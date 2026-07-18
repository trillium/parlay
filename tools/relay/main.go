// Command relay is the single central fan-out for Parlay agent channels.
//
// Instead of N independent bun poll loops (one ~40MB process per agent), ONE
// relay process holds one upstream long-poll loop per registered agent against
// the Pulse chat server, and appends each inbound user message to that agent's
// private spool file as a CHAT_MSG line. Each agent's `parlay monitor` then just
// tails its spool file — a ~1.2MB `tail -F`, not a 40MB poller.
//
// Wire contract (see tools/RELAY_MONITOR.md):
//
//	Upstream poll : GET  {server}/api/chat/poll?after=<lastId>&channel=<agent>
//	                → {"timeout":true}  or  {"id","role","text","ts",...}
//	Spool line    : CHAT_MSG|<id>|<role>|<text>\n               (captain messages, no attribution)
//	              : CHAT_MSG|<id>|<role>|<text>|from:<sender>\n (agent→agent messages, 5th field)
//	Spool path    : {runtime-dir}/<agent>.chan       (runtime-dir defaults to $TMPDIR/parlay)
//	Control socket : Unix domain socket at {runtime-dir}/relay.sock
//	  POST /register {"agent":"<id>"}     → {"ok":true,"agent":"<id>","spool":"<path>"}   (idempotent)
//	  POST /unregister {"agent":"<id>"}   → {"ok":true}
//	  GET  /agents                        → {"agents":[...],"server":"...","runtime":"..."}
//	  GET  /health                        → {"ok":true}
//
// Exit codes: 0 clean shutdown (SIGINT/SIGTERM), 1 fatal startup error.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// defaultServer matches bin/parlay: the Pulse server on port 31337.
const defaultServer = "http://localhost:31337"

// pollTimeout bounds a single upstream long-poll HTTP request. The server's own
// long-poll times out at 30s and returns {"timeout":true}; we allow a margin so
// a slow-but-alive server is never mistaken for a dead one.
const pollTimeout = 45 * time.Second

// reconnectDelay is the backoff after an upstream error before the next poll,
// so a down server does not spin the CPU. Bounded and small so recovery is fast.
const reconnectDelay = 2 * time.Second

// upstreamMessage is one message from the server's poll endpoint. A timeout tick
// sets Timeout=true and leaves the message fields empty.
type upstreamMessage struct {
	Timeout bool   `json:"timeout"`
	ID      string `json:"id"`
	Role    string `json:"role"`
	Text    string `json:"text"`
	Ts      string `json:"ts"`
	From    string `json:"from"` // sender attribution; empty = captain
}

// agentLoop owns one agent's upstream poll goroutine and its spool file.
type agentLoop struct {
	id     string
	spool  string
	cancel context.CancelFunc
	done   chan struct{} // closed when the goroutine has fully exited
}

// relay is the whole daemon: registry + shared config. Guarded by mu.
type relay struct {
	server     string
	runtimeDir string
	client     *http.Client

	mu     sync.Mutex
	loops  map[string]*agentLoop
	closed bool // set once shutdown begins; blocks new registrations
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("relay: ")

	var (
		serverFlag  = flag.String("server", envOr("PARLAY_SERVER", defaultServer), "Pulse chat server base URL")
		runtimeFlag = flag.String("runtime-dir", defaultRuntimeDir(), "directory for spool files and the control socket")
		agentsFlag  = flag.String("agents", "", "comma-separated agent ids to register at startup (optional)")
	)
	flag.Parse()

	server := strings.TrimRight(*serverFlag, "/")
	if server == "" {
		log.Fatal("server URL is empty")
	}
	runtimeDir := *runtimeFlag
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		log.Fatalf("cannot create runtime dir %s: %v", runtimeDir, err)
	}

	r := &relay{
		server:     server,
		runtimeDir: runtimeDir,
		// One client, no total timeout (the per-request context bounds polls).
		client: &http.Client{},
		loops:  make(map[string]*agentLoop),
	}

	// Register any startup agents before serving so they are live immediately.
	for _, id := range splitAgents(*agentsFlag) {
		if _, err := r.register(id); err != nil {
			log.Fatalf("startup register %q: %v", id, err)
		}
		log.Printf("registered agent %q at startup", id)
	}

	// Resume agents from existing spools. The registry is in-memory only, so a
	// relay restart would otherwise silently stop every enrolled agent's
	// upstream poll loop while their monitors keep tailing dead spools —
	// observed fleet-wide on 2026-07-17 (19 agents deaf until hand re-enrolled).
	// A spool file is durable evidence of enrollment; re-register it at boot.
	// register() is idempotent, so overlap with --agents is harmless.
	if entries, err := os.ReadDir(runtimeDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".chan") {
				continue
			}
			id := strings.TrimSuffix(name, ".chan")
			if !validAgentID(id) {
				continue
			}
			if _, err := r.register(id); err != nil {
				log.Printf("spool-resume register %q: %v", id, err)
				continue
			}
			log.Printf("resumed agent %q from spool", id)
		}
	}

	sockPath := filepath.Join(runtimeDir, "relay.sock")
	ln, err := listenControl(sockPath)
	if err != nil {
		log.Fatalf("cannot bind control socket %s: %v", sockPath, err)
	}

	srv := &http.Server{Handler: r.controlMux()}

	// Serve the control socket until shutdown.
	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	log.Printf("up — server=%s runtime=%s socket=%s", server, runtimeDir, sockPath)

	// Wait for a termination signal or a fatal serve error.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case s := <-sig:
		log.Printf("received %s — shutting down", s)
	case err := <-serveErr:
		if err != nil {
			log.Printf("control socket failed: %v — shutting down", err)
		}
	}

	r.shutdown(srv)
	_ = os.Remove(sockPath) // best-effort: leave no stale socket behind
	log.Print("stopped")
}

// ── Registry ──────────────────────────────────────────────────────────────────

// register adds an agent to the registry and starts its poll loop. Idempotent:
// registering an already-registered agent returns its existing spool path and
// does not start a second loop. Returns the agent's spool file path.
func (r *relay) register(agent string) (string, error) {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return "", errors.New("agent id is empty")
	}
	if !validAgentID(agent) {
		return "", fmt.Errorf("invalid agent id %q (want kebab-slug)", agent)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return "", errors.New("relay is shutting down")
	}
	if existing, ok := r.loops[agent]; ok {
		return existing.spool, nil // idempotent
	}

	spool := filepath.Join(r.runtimeDir, agent+".chan")
	// Ensure the spool file exists so a monitor can `tail -F` it even before the
	// first message arrives. O_APPEND across relay restarts preserves any queued
	// lines a still-running monitor has not yet consumed.
	f, err := os.OpenFile(spool, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("open spool %s: %w", spool, err)
	}
	_ = f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	loop := &agentLoop{id: agent, spool: spool, cancel: cancel, done: make(chan struct{})}
	r.loops[agent] = loop
	go r.pollLoop(ctx, loop)
	log.Printf("agent %q registered — spool %s", agent, spool)
	return spool, nil
}

// unregister stops an agent's poll loop and removes it from the registry. The
// spool file is left on disk so a lagging monitor can drain it; a fresh
// register reuses it. Idempotent: unregistering an unknown agent is a no-op.
func (r *relay) unregister(agent string) {
	r.mu.Lock()
	loop, ok := r.loops[agent]
	if ok {
		delete(r.loops, agent)
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	loop.cancel()
	<-loop.done
	log.Printf("agent %q unregistered", agent)
}

// agentIDs returns the currently registered agent ids (sorted for stable output).
func (r *relay) agentIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.loops))
	for id := range r.loops {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return ids
}

// shutdown cancels every poll loop, waits for them, then stops the control HTTP
// server. Registration is blocked for the duration.
func (r *relay) shutdown(srv *http.Server) {
	r.mu.Lock()
	r.closed = true
	loops := make([]*agentLoop, 0, len(r.loops))
	for _, l := range r.loops {
		loops = append(loops, l)
	}
	r.loops = make(map[string]*agentLoop)
	r.mu.Unlock()

	for _, l := range loops {
		l.cancel()
	}
	for _, l := range loops {
		<-l.done
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// ── Poll loop ───────────────────────────────────────────────────────────────

// pollLoop long-polls the upstream server for one agent's channel and appends
// each inbound user message to the agent's spool file. It runs until ctx is
// cancelled (unregister or shutdown). It never returns on transient errors; it
// backs off and retries, because a Parlay agent must survive a server restart.
func (r *relay) pollLoop(ctx context.Context, loop *agentLoop) {
	defer close(loop.done)

	// Resume after the last message already written to the spool. On a relay
	// restart the server would otherwise replay the whole channel history from
	// after="", duplicating every already-spooled line. Seeding lastID from the
	// spool makes the restart re-open exactly-once from the monitor's point of
	// view (the monitor's tail -F never restarts).
	lastID := lastSpooledID(loop.spool)
	if lastID != "" {
		log.Printf("agent %q resuming after spooled id %s", loop.id, lastID)
	}
	for {
		if ctx.Err() != nil {
			return
		}
		msg, err := r.pollOnce(ctx, loop.id, lastID)
		if err != nil {
			if ctx.Err() != nil {
				return // cancellation, not a real error
			}
			log.Printf("agent %q poll error: %v (retry in %s)", loop.id, err, reconnectDelay)
			if !sleepCtx(ctx, reconnectDelay) {
				return
			}
			continue
		}
		if msg == nil || msg.Timeout {
			continue // idle tick — poll again immediately, server holds the connection
		}
		if msg.ID == "" || msg.Role == "" {
			// Defensive: a malformed message would corrupt lastID advancement.
			log.Printf("agent %q: skipping message with empty id/role", loop.id)
			continue
		}
		if msg.Role != "user" && msg.Role != "agent" {
			// Device-level events (role "tts_event", …) are resolved into poll
			// waiters by the server but are NOT chat history: /api/chat/poll's
			// after-index does not know their ids, so advancing lastID to one
			// resets the upstream cursor to -1 and replays the channel's whole
			// backlog (observed live 2026-07-17: an old captain message was
			// redelivered after every TTS event). Don't spool, don't advance.
			continue
		}
		lastID = msg.ID
		if err := appendSpool(loop.spool, msg); err != nil {
			// A spool write failure is not fatal to the loop — log and keep polling
			// so a transient disk issue does not silently kill the agent's channel.
			log.Printf("agent %q: spool write failed: %v", loop.id, err)
		}
	}
}

// pollOnce performs one upstream long-poll request. A per-request context bounds
// it to pollTimeout so a wedged server cannot block the loop forever.
func (r *relay) pollOnce(parent context.Context, agent, after string) (*upstreamMessage, error) {
	ctx, cancel := context.WithTimeout(parent, pollTimeout)
	defer cancel()

	q := fmt.Sprintf("%s/api/chat/poll?after=%s&channel=%s",
		r.server, urlValue(after), urlValue(agent))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Drain a bounded amount of the body so the connection can be reused.
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		return nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
	}
	var msg upstreamMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &msg, nil
}

// lastSpooledID returns the id from the last well-formed CHAT_MSG line in the
// spool, or "" if the spool is empty/absent/unparseable. Used to resume polling
// after a relay restart without replaying the channel's whole history. Only the
// file's tail is read so a large spool does not force a full scan.
func lastSpooledID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "" // no spool yet — start fresh
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return ""
	}
	// Read up to the last 64KiB — far more than any single line, so the final
	// complete line is guaranteed to be within it.
	const window = 64 * 1024
	size := info.Size()
	start := int64(0)
	if size > window {
		start = size - window
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return ""
	}
	// Walk lines from the end; return the id of the last that parses as a
	// CHAT_MSG. A partial first line (when start > 0) is naturally skipped
	// because we only accept lines that begin with the exact "CHAT_MSG|" prefix.
	text := string(buf)
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimRight(lines[i], "\r")
		if !strings.HasPrefix(line, "CHAT_MSG|") {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		// Only chat-history roles seed the cursor — spools written before the
		// tts_event filter existed may end in event lines whose ids the poll
		// after-index cannot resolve (cursor reset → backlog replay).
		if len(parts) >= 3 && parts[1] != "" && (parts[2] == "user" || parts[2] == "agent") {
			return parts[1]
		}
	}
	return ""
}

// appendSpool writes one CHAT_MSG line for msg to the agent's spool file.
// Newlines in the text are flattened to spaces so each message is exactly one
// line — the monitor's line-oriented reader depends on it. The write is O_APPEND
// so concurrent relays or a re-registered loop never truncate each other.
func appendSpool(path string, msg *upstreamMessage) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	fromSuffix := ""
	if msg.From != "" {
		fromSuffix = "|from:" + msg.From
	}
	line := fmt.Sprintf("CHAT_MSG|%s|%s|%s%s\n", msg.ID, msg.Role, flatten(msg.Text), fromSuffix)
	if _, err := f.WriteString(line); err != nil {
		return err
	}
	return nil
}

// ── Control socket ──────────────────────────────────────────────────────────

// controlMux is the HTTP handler served over the Unix control socket.
func (r *relay) controlMux() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("/agents", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET only"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"agents":  r.agentIDs(),
			"server":  r.server,
			"runtime": r.runtimeDir,
		})
	})

	mux.HandleFunc("/register", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
			return
		}
		agent, err := decodeAgentBody(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		spool, err := r.register(agent)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "agent": agent, "spool": spool})
	})

	mux.HandleFunc("/unregister", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
			return
		}
		agent, err := decodeAgentBody(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		r.unregister(agent)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "agent": agent})
	})

	return mux
}

// decodeAgentBody extracts and validates the {"agent":"<id>"} field.
func decodeAgentBody(req *http.Request) (string, error) {
	var body struct {
		Agent string `json:"agent"`
	}
	dec := json.NewDecoder(io.LimitReader(req.Body, 4096))
	if err := dec.Decode(&body); err != nil {
		return "", fmt.Errorf("bad JSON body: %w", err)
	}
	agent := strings.TrimSpace(body.Agent)
	if agent == "" {
		return "", errors.New("agent id is required")
	}
	if !validAgentID(agent) {
		return "", fmt.Errorf("invalid agent id %q (want kebab-slug)", agent)
	}
	return agent, nil
}

// listenControl binds the Unix domain control socket, removing any stale socket
// left by a previous crashed relay first.
func listenControl(path string) (net.Listener, error) {
	// A leftover socket file from an unclean exit would make Listen fail with
	// EADDRINUSE even though nothing is listening; remove it first. If a live
	// relay is already bound, the subsequent Listen still fails and we surface it.
	if _, err := os.Stat(path); err == nil {
		if probeAlive(path) {
			return nil, fmt.Errorf("another relay is already listening on %s", path)
		}
		_ = os.Remove(path)
	}
	return net.Listen("unix", path)
}

// probeAlive returns true if something is already accepting on the Unix socket.
func probeAlive(path string) bool {
	c, err := net.DialTimeout("unix", path, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// flatten collapses all newline forms to single spaces so a message is one line.
func flatten(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// validAgentID enforces the kebab-slug shape used everywhere in Parlay, and
// rejects anything that could escape the runtime dir as a path component.
func validAgentID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	prevDash := true // leading dash not allowed (prevDash starts true)
	for _, ch := range s {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			prevDash = false
		case ch == '-':
			if prevDash {
				return false // no leading or double dash
			}
			prevDash = true
		default:
			return false
		}
	}
	return !prevDash // no trailing dash
}

// urlValue percent-encodes a query value. Agent ids are kebab-slugs and after is
// a UUID, so this is defensive; it keeps the request well-formed regardless.
func urlValue(s string) string {
	// Minimal, allocation-light encoding for the characters that actually appear
	// (alnum, '-'). Anything unexpected is escaped so the URL never breaks.
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// sleepCtx sleeps for d unless ctx is cancelled first. Returns false if cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// defaultRuntimeDir is $TMPDIR/parlay (falls back to /tmp/parlay).
func defaultRuntimeDir() string {
	base := os.Getenv("TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	return filepath.Join(base, "parlay")
}

func splitAgents(csv string) []string {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sortStrings(s []string) {
	// Tiny insertion sort avoids importing sort for one call in a footprint-lean
	// binary; the registry is a handful of agents at most.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		// Response already partly written; nothing actionable but log it.
		log.Printf("control response encode failed: %v", err)
	}
}
