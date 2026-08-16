package handlers

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── Server TTS (port of tts.ts) ────────────────────────────────────────────
//
// POST /api/chat/tts synthesizes text to audio/wav through the local speak
// daemon over its Unix socket (length-prefixed JSON in/out, base64 WAV back),
// with a substitution map and a disk clip cache layered in front. tts-report /
// tts-correction persist pronunciation feedback; tts-event relays playback
// lifecycle to SSE. This is the Go port of the same routes, so the panel's
// speech path is unchanged after the flip.

const ttsSubsPath = "tts-substitutions.json" // resolved against the repo's packages/server/src

var subsPathCache struct {
	sync.Mutex
	path string
}

func substitutionsPath() string {
	subsPathCache.Lock()
	defer subsPathCache.Unlock()
	if subsPathCache.path != "" {
		return subsPathCache.path
	}
	// Prefer the TS server's committed substitutions file; it is the source of
	// truth for pronunciation fixes. Resolve relative to the go-server module's
	// repo root (../../ from internal/handlers to the repo root, then
	// packages/server/src).
	wd, _ := os.Getwd()
	for _, cand := range []string{
		filepath.Join(wd, "..", "..", "packages", "server", "src", "tts-substitutions.json"),
		filepath.Join(wd, "packages", "server", "src", "tts-substitutions.json"),
	} {
		if _, err := os.Stat(cand); err == nil {
			subsPathCache.path = cand
			return cand
		}
	}
	subsPathCache.path = ttsSubsPath
	return subsPathCache.path
}

var subsCache struct {
	sync.Mutex
	mtime   int64
	version int64
	subs    map[string]string
}

func substitutionMap() map[string]string {
	subsCache.Lock()
	defer subsCache.Unlock()
	fi, err := os.Stat(substitutionsPath())
	if err != nil {
		subsCache.subs = map[string]string{}
		subsCache.mtime = 0
		return subsCache.subs
	}
	if fi.ModTime().UnixNano() == subsCache.mtime && subsCache.subs != nil {
		return subsCache.subs
	}
	b, err := os.ReadFile(substitutionsPath())
	m := map[string]string{}
	if err == nil {
		_ = json.Unmarshal(b, &m)
	}
	subsCache.subs = m
	subsCache.mtime = fi.ModTime().UnixNano()
	subsCache.version = fi.ModTime().UnixNano()
	return subsCache.subs
}

// normalizeForSpeech mirrors tts.ts's version-string reading ("v3.7.1" →
// "v 3 point 7 point 1") and dotted-version normalization. The TS regexes use
// lookarounds to keep IPs and longer dotted sequences untouched; Go's RE2 has
// no lookbehind, so the boundary checks are done in code instead.
var reDotted3 = regexp.MustCompile(`(?i)(v?)(\d+)\.(\d+)\.(\d+)`)
var reDotted2 = regexp.MustCompile(`(?i)v(\d+)\.(\d+)`)

func isVersionBoundary(s string, i int) bool {
	if i > 0 {
		c := s[i-1]
		if c == '.' || (c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func isVersionEnd(s string, i int) bool {
	if i < len(s) {
		c := s[i]
		if c == '.' || (c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func normalizeForSpeech(text string) string {
	// Scan and rewrite both version forms with boundary checks, right-to-left
	// so earlier replacements do not shift later indices.
	type span struct {
		start, end int
		repl       string
	}
	var spans []span

	for _, loc := range reDotted3.FindAllStringSubmatchIndex(text, -1) {
		if !isVersionBoundary(text, loc[0]) || !isVersionEnd(text, loc[1]) {
			continue
		}
		v := text[loc[2]:loc[3]]
		a := text[loc[4]:loc[5]]
		b := text[loc[6]:loc[7]]
		c := text[loc[8]:loc[9]]
		prefix := ""
		if v != "" {
			prefix = "v "
		}
		spans = append(spans, span{loc[0], loc[1], prefix + a + " point " + b + " point " + c})
	}
	for _, loc := range reDotted2.FindAllStringSubmatchIndex(text, -1) {
		if !isVersionBoundary(text, loc[0]) || !isVersionEnd(text, loc[1]) {
			continue
		}
		a := text[loc[2]:loc[3]]
		b := text[loc[4]:loc[5]]
		spans = append(spans, span{loc[0], loc[1], "v " + a + " point " + b})
	}

	if len(spans) == 0 {
		return text
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	out := text
	for _, s := range spans {
		out = out[:s.start] + s.repl + out[s.end:]
	}
	return out
}

func applySubstitutions(text string) string {
	out := normalizeForSpeech(text)
	for from, to := range substitutionMap() {
		esc := regexp.QuoteMeta(from)
		re := regexp.MustCompile(`(?i)\b` + esc + `\b`)
		out = re.ReplaceAllString(out, to)
	}
	return out
}

// ── Speak daemon client ─────────────────────────────────────────────────────

func speakSocketPath() string {
	account := os.Getenv("USER")
	if account == "" {
		account = os.Getenv("LOGNAME")
	}
	if account == "" {
		if u, err := user.Current(); err == nil {
			account = u.Username
		}
	}
	return "/tmp/speak-" + account + ".sock"
}

const synthTimeout = 30 * time.Second

// synthViaDaemon sends a length-prefixed JSON command to the speak daemon and
// returns the decoded base64 WAV, or an error.
func synthViaDaemon(payload map[string]any) ([]byte, error) {
	conn, err := net.DialTimeout("unix", speakSocketPath(), synthTimeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(synthTimeout))

	body, _ := json.Marshal(payload)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(body)))
	if _, err := conn.Write(append(lenBuf[:], body...)); err != nil {
		return nil, err
	}

	// Read the 4-byte length prefix, then the JSON body.
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return nil, err
	}
	msgLen := binary.BigEndian.Uint32(hdr[:])
	msg := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, msg); err != nil {
		return nil, err
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		WavB64 string `json:"wav_b64"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errFromString(resp.Error)
	}
	return base64.StdEncoding.DecodeString(resp.WavB64)
}

type synthError struct{ msg string }

func (e *synthError) Error() string { return e.msg }

func errFromString(s string) error {
	if s == "" {
		s = "synth failed"
	}
	return &synthError{msg: s}
}

// ── Disk clip cache ─────────────────────────────────────────────────────────

func ttsCacheDir() string {
	pai := os.Getenv("PAI_DIR")
	if pai == "" {
		home, _ := os.UserHomeDir()
		pai = filepath.Join(home, ".claude", "PAI")
	}
	return filepath.Join(pai, "MEMORY", "STATE", "tts-cache")
}

func diskClipKey(key string) string {
	h := sha1.Sum([]byte(key))
	return hex.EncodeToString(h[:])[:24] + ".wav"
}

func diskGet(key string) ([]byte, bool) {
	p := filepath.Join(ttsCacheDir(), diskClipKey(key))
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now) // LRU touch
	return b, true
}

func diskPut(key string, wav []byte) {
	dir := ttsCacheDir()
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, diskClipKey(key)), wav, 0o644)
	// Prune to the 100 most-recent clips.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type clip struct {
		name  string
		mtime time.Time
	}
	var clips []clip
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".wav") {
			if fi, err := e.Info(); err == nil {
				clips = append(clips, clip{e.Name(), fi.ModTime()})
			}
		}
	}
	sort.Slice(clips, func(i, j int) bool { return clips[i].mtime.After(clips[j].mtime) })
	for _, c := range clips[100:] {
		_ = os.Remove(filepath.Join(dir, c.name))
	}
}

var clipCache struct {
	sync.Mutex
	m map[string][]byte
}

func init() { clipCache.m = make(map[string][]byte) }

const clipCacheMax = 40

// handleTTS implements POST /api/chat/tts — synth to audio/wav.
func handleTTS() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			Text  string  `json:"text"`
			Voice string  `json:"voice"`
			Speed float64 `json:"speed"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		raw := strings.TrimSpace(body.Text)
		if raw == "" {
			writeStatusError(w, http.StatusBadRequest, "text required")
			return
		}
		if len(raw) > 2000 {
			raw = raw[:2000]
		}
		text := applySubstitutions(raw)
		key := body.Voice + "|" + strconv.FormatFloat(body.Speed, 'f', -1, 64) + "|" + text

		wav, ok := clipCacheLookup(key)
		if !ok {
			if b, found := diskGet(key); found {
				wav = b
			}
		}
		if wav == nil {
			payload := map[string]any{"text": text, "caller": "parlay"}
			if body.Voice != "" {
				payload["voice"] = body.Voice
			}
			if body.Speed != 0 {
				payload["speed"] = body.Speed
			}
			b, err := synthViaDaemon(payload)
			if err != nil {
				writeStatusError(w, http.StatusBadGateway, err.Error())
				return
			}
			wav = b
			diskPut(key, wav)
		}
		clipCacheStore(key, wav)

		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(wav)
	}
}

func clipCacheLookup(key string) ([]byte, bool) {
	clipCache.Lock()
	defer clipCache.Unlock()
	b, ok := clipCache.m[key]
	return b, ok
}

func clipCacheStore(key string, wav []byte) {
	clipCache.Lock()
	defer clipCache.Unlock()
	clipCache.m[key] = wav
	if len(clipCache.m) > clipCacheMax {
		for k := range clipCache.m {
			delete(clipCache.m, k)
			break
		}
	}
}

// ttsReportsPath is where pronunciation reports land (appended JSONL).
func ttsReportsPath() string {
	pai := os.Getenv("PAI_DIR")
	if pai == "" {
		home, _ := os.UserHomeDir()
		pai = filepath.Join(home, ".claude", "PAI")
	}
	return filepath.Join(pai, "MEMORY", "OBSERVABILITY", "tts-pronunciation-reports.jsonl")
}

func appendReport(entry map[string]any) {
	f, err := os.OpenFile(ttsReportsPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(entry)
	_, _ = f.Write(append(b, '\n'))
}

// handleTTSReport implements POST /api/chat/tts-report — mispronunciation flags.
func handleTTSReport() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			Sentence string `json:"sentence"`
			Voice    string `json:"voice"`
			ClipMeta any    `json:"clipMeta"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		sentence := strings.TrimSpace(body.Sentence)
		if sentence == "" {
			writeAppError(w, "sentence required")
			return
		}
		if len(sentence) > 500 {
			sentence = sentence[:500]
		}
		voice := body.Voice
		if voice == "" {
			voice = "parlay-pool"
		}
		appendReport(map[string]any{
			"ts":       time.Now().UTC().Format(time.RFC3339Nano),
			"sentence": sentence,
			"voice":    voice,
			"clipMeta": body.ClipMeta,
		})
		writeJSON(w, map[string]any{"ok": true})
	}
}

// handleTTSCorrection implements POST /api/chat/tts-correction — persist a
// pronunciation substitution so the fix takes effect immediately.
func handleTTSCorrection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			From     string `json:"from"`
			To       string `json:"to"`
			Sentence string `json:"sentence"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		from := strings.TrimSpace(body.From)
		to := strings.TrimSpace(body.To)
		if from == "" || to == "" {
			writeAppError(w, "from and to required")
			return
		}
		if len(from) > 100 {
			from = from[:100]
		}
		if len(to) > 200 {
			to = to[:200]
		}
		subs := substitutionMap()
		subs[from] = to
		b, _ := json.MarshalIndent(subs, "", "  ")
		if err := os.WriteFile(substitutionsPath(), append(b, '\n'), 0o644); err != nil {
			writeStatusError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Invalidate the mtime cache so the next synth picks it up.
		subsCache.Lock()
		subsCache.mtime = 0
		subsCache.Unlock()

		sentence := strings.TrimSpace(body.Sentence)
		if len(sentence) > 500 {
			sentence = sentence[:500]
		}
		appendReport(map[string]any{
			"ts":       time.Now().UTC().Format(time.RFC3339Nano),
			"sentence": sentence,
			"voice":    "parlay-pool",
			"clipMeta": map[string]any{"kind": "correction", "from": from, "to": to},
		})
		writeJSON(w, map[string]any{"ok": true, "substitutions": len(subs)})
	}
}

// handleTTSEvent implements POST /api/chat/tts-event — relay a playback
// lifecycle event to every SSE client as `tts_event`.
func handleTTSEvent(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body map[string]any
		if !decodeJSON(w, r, &body) {
			return
		}
		eventType := "unknown"
		if t, ok := body["type"].(string); ok {
			eventType = t
		}
		device := "unknown"
		if d, ok := body["device"].(string); ok {
			device = d
		}
		msg := map[string]any{
			"id":     newRPCID(),
			"role":   "tts_event",
			"type":   eventType,
			"device": device,
			"ts":     time.Now().UTC().Format(time.RFC3339Nano),
		}
		for k, v := range body {
			if k == "type" || k == "device" {
				continue
			}
			msg[k] = v
		}
		hub.broadcast("tts_event", msg)
		writeJSON(w, map[string]any{"ok": true})
	}
}
