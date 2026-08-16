package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"parlay/go-server/internal/store"
)

// ── Observability tailers (Go-native port of tool-tailer.ts / hook-tailer.ts) ─
//
// The TS server runs two tailers that watch JSONL files under $PAI_DIR and turn
// them into panel events: tool-activity.jsonl → tool_event, hook-firings.jsonl
// → system_update messages. After the flip the TS process is gone, so these
// tailers live HERE, tailing the same files in-process and broadcasting
// directly into this server's hub / message store — no HTTP ingress round-trip.
//
// Both tailers must never crash the server: every read/parse failure is
// swallowed, and a missing file simply means the tailer waits for it to appear.

// enrollmentRe extracts the `--agent <channel>` an enrollment command targets,
// matching the TS side's ENROLL_RE: `--agent foo` and `--agent=foo`, a
// kebab/underscore slug. Only a real `parlay monitor` invocation counts — a
// Bash line that merely mentions the command must not remap a session.
var enrollmentRe = regexp.MustCompile(`(?i)parlay\s+monitor\b[^\n]*?--agent[=\s]+["']?([a-z0-9][a-z0-9_-]*)`)

// sessionChannels is the session_id → channel map shared by the tailers and
// the declare-channel route. Two layers, exactly as the TS session-channel.ts:
// a primary JSON declaration file (written by agents via declare-channel or
// directly), plus an in-memory fallback learned from tool-activity enrollment
// lines. The fallback is sticky (first-enrollment-wins): a session's identity
// is set at spawn, and later monitors of OTHER channels are that agent
// watching others, not becoming them.
type sessionChannels struct {
	mu           sync.Mutex
	fallback     map[string]string
	declFile     string
	stateFile    string
	toolActivity string
	hookFirings  string
}

func newSessionChannels() *sessionChannels {
	pai := os.Getenv("PAI_DIR")
	if pai == "" {
		home, _ := os.UserHomeDir()
		pai = filepath.Join(home, ".claude", "PAI")
	}
	exchange := filepath.Join(homeDir(), "exchange")

	sc := &sessionChannels{
		fallback:     make(map[string]string),
		declFile:     filepath.Join(exchange, "parlay-agent-channels.json"),
		stateFile:    filepath.Join(pai, "MEMORY", "STATE", "parlay-session-channels.json"),
		toolActivity: filepath.Join(pai, "MEMORY", "OBSERVABILITY", "tool-activity.jsonl"),
		hookFirings:  filepath.Join(pai, "MEMORY", "OBSERVABILITY", "hook-firings.jsonl"),
	}
	sc.loadState()
	return sc
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// loadState hydrates the fallback map from the persisted state file, so
// enrollments survive a server restart.
func (sc *sessionChannels) loadState() {
	b, err := os.ReadFile(sc.stateFile)
	if err != nil {
		return
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil {
		return
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for sid, ch := range m {
		if sid != "" && ch != "" {
			sc.fallback[sid] = ch
		}
	}
}

func (sc *sessionChannels) persist() {
	sc.mu.Lock()
	cp := make(map[string]string, len(sc.fallback))
	for k, v := range sc.fallback {
		cp[k] = v
	}
	sc.mu.Unlock()

	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(sc.stateFile), 0o755)
	_ = os.WriteFile(sc.stateFile, append(b, '\n'), 0o644)
}

// readDeclarations returns the primary JSON declaration file's session→channel
// map, empty on any read/parse failure (best-effort, never throws).
func (sc *sessionChannels) readDeclarations() map[string]string {
	b, err := os.ReadFile(sc.declFile)
	if err != nil {
		return nil
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

// declareChannel writes a session→channel declaration to the primary JSON file.
// Sticky: first declaration wins, matching the TS behavior.
func (sc *sessionChannels) declareChannel(sessionID, channel string) {
	if sessionID == "" || channel == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(sc.declFile), 0o755)
	existing := sc.readDeclarations()
	if existing == nil {
		existing = make(map[string]string)
	}
	if existing[sessionID] != "" {
		return
	}
	existing[sessionID] = channel
	if b, err := json.MarshalIndent(existing, "", "  "); err == nil {
		_ = os.WriteFile(sc.declFile, append(b, '\n'), 0o644)
	}
}

// recordSessionChannel stores a fallback mapping, sticky (first wins).
func (sc *sessionChannels) recordSessionChannel(sessionID, channel string) {
	if sessionID == "" || channel == "" {
		return
	}
	sc.mu.Lock()
	if sc.fallback[sessionID] != "" {
		sc.mu.Unlock()
		return
	}
	sc.fallback[sessionID] = channel
	sc.mu.Unlock()
	sc.persist()
}

// channelForSession resolves a session's owning tab: declaration file first,
// then the learned fallback, else "" (caller maps to "system").
func (sc *sessionChannels) channelForSession(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	if decl := sc.readDeclarations(); decl[sessionID] != "" {
		return decl[sessionID]
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.fallback[sessionID]
}

// ── Tail helpers ────────────────────────────────────────────────────────────

// tailFile reads the bytes appended to path after offset, returning the new
// offset and the raw chunk. On any error it returns the original offset and
// nil, so a rotating/missing file is simply skipped this tick.
func tailFile(path string, offset int64) (int64, []byte) {
	fi, err := os.Stat(path)
	if err != nil {
		return offset, nil
	}
	if fi.Size() <= offset {
		return offset, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return offset, nil
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, nil
	}
	buf := make([]byte, fi.Size()-offset)
	n, err := io.ReadFull(f, buf)
	if err != nil {
		return offset, nil
	}
	return fi.Size(), buf[:n]
}

// startToolEventTailer tails tool-activity.jsonl every 500ms and broadcasts
// each new entry as a tool_event, scoped to the owning agent's channel.
func startToolEventTailer(sc *sessionChannels, hub *Hub) {
	var offset int64
	if fi, err := os.Stat(sc.toolActivity); err == nil {
		offset = fi.Size()
	}

	go func() {
		for {
			newOffset, chunk := tailFile(sc.toolActivity, offset)
			if newOffset > offset {
				offset = newOffset
				for _, line := range splitLines(chunk) {
					var ev struct {
						SessionID        string `json:"session_id"`
						Timestamp        string `json:"timestamp"`
						ToolName         string `json:"tool_name"`
						ToolInputPreview string `json:"tool_input_preview"`
						GroundTruth      *struct {
							Description   string `json:"description"`
							Command       string `json:"command"`
							StdoutPreview string `json:"stdout_preview"`
							StderrPreview string `json:"stderr_preview"`
						} `json:"ground_truth"`
					}
					if json.Unmarshal(line, &ev) != nil {
						continue
					}
					// Learn session→channel from the agent's own Monitor
					// enrollment BEFORE attributing this or any later event.
					if ev.ToolName == "Monitor" {
						if ch := enrollmentChannel(ev.ToolInputPreview); ch != "" {
							sc.recordSessionChannel(ev.SessionID, ch)
						}
					}
					gt := ev.GroundTruth
					desc, cmd, out, errStr := "", "", "", ""
					if gt != nil {
						desc, cmd, out, errStr = gt.Description, gt.Command, gt.StdoutPreview, gt.StderrPreview
					}
					channel := sc.channelForSession(ev.SessionID)
					if channel == "" {
						channel = "system"
					}
					hub.broadcast(eventToolEvent, map[string]any{
						"ts":      ev.Timestamp,
						"tool":    orEmpty(ev.ToolName, "?"),
						"desc":    truncate(desc, 100),
						"cmd":     truncate(cmd, 140),
						"out":     truncate(out, 280),
						"err":     truncate(errStr, 120),
						"channel": channel,
					})
				}
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()
}

// startHookFiringTailer tails hook-firings.jsonl every second and turns each
// entry into a system_update chat message, persisted + broadcast by
// appendAndPublish (the same path the TS hook tailer used via POST /message).
func startHookFiringTailer(sc *sessionChannels, st *store.Store, b *broker) {
	var offset int64
	if fi, err := os.Stat(sc.hookFirings); err == nil {
		offset = fi.Size()
	}

	go func() {
		for {
			newOffset, chunk := tailFile(sc.hookFirings, offset)
			if newOffset < offset {
				offset = 0 // rotated/truncated — restart from the top
				continue
			}
			if newOffset > offset {
				offset = newOffset
				for _, line := range splitLines(chunk) {
					var ev struct {
						SessionID string `json:"session_id"`
						Source    string `json:"source"`
						Text      string `json:"text"`
					}
					if json.Unmarshal(line, &ev) != nil {
						continue
					}
					text := truncate(ev.Text, 1400)
					if text == "" {
						continue
					}
					channel := sc.channelForSession(ev.SessionID)
					if channel == "" {
						channel = "system"
					}
					msg := store.ChatMessage{
						Role:    "agent",
						Text:    text,
						Channel: channel,
						Type:    "system_update",
						Source:  truncate(ev.Source, 60),
					}
					if ev.SessionID != "" {
						msg.Meta = map[string]any{"session_id": ev.SessionID}
					}
					_, _, _ = appendAndPublish(st, b, msg)
				}
			}
			time.Sleep(time.Second)
		}
	}()
}

// backfillFromToolActivity scans the tail of tool-activity.jsonl once at boot
// so sessions that enrolled before this server started map immediately. The
// live tailer resumes at EOF and would otherwise never re-see them.
func backfillFromToolActivity(sc *sessionChannels) {
	b, err := os.ReadFile(sc.toolActivity)
	if err != nil {
		return
	}
	const tailBytes = 512 * 1024
	if len(b) > tailBytes {
		b = b[len(b)-tailBytes:]
	}
	for _, line := range splitLines(b) {
		if !bytes.Contains(line, []byte("parlay monitor")) {
			continue
		}
		var ev struct {
			SessionID        string `json:"session_id"`
			ToolName         string `json:"tool_name"`
			ToolInputPreview string `json:"tool_input_preview"`
		}
		if json.Unmarshal(line, &ev) != nil || ev.ToolName != "Monitor" {
			continue
		}
		if ch := enrollmentChannel(ev.ToolInputPreview); ch != "" {
			sc.recordSessionChannel(ev.SessionID, ch)
		}
	}
}

func enrollmentChannel(text string) string {
	m := enrollmentRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			line := b[start:i]
			if len(line) > 0 {
				cp := make([]byte, len(line))
				copy(cp, line)
				out = append(out, cp)
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

func orEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
