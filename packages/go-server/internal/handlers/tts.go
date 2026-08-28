package handlers

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	diskCacheMax = 100
	clipCacheMax = 40
	textMaxLen   = 2000
	subMaxLen    = 200
	reportMaxLen = 500
)

// TTSHandler manages TTS synthesis with caching and pronunciation substitutions.
type TTSHandler struct {
	engine       TTSEngine
	diskCacheDir string
	clipCache    map[string][]byte
	clipMutex    sync.RWMutex
	subsPath     string
	subs         struct {
		mu    sync.RWMutex
		mtime int64
		map_  map[string]string
	}
	reportPath string
}

// NewTTSHandler creates a new TTS handler with the given engine and configuration.
func NewTTSHandler(engine TTSEngine, paiDir string) *TTSHandler {
	subsPath := filepath.Join(paiDir, "tts-substitutions.json")
	diskCacheDir := filepath.Join(paiDir, "MEMORY", "STATE", "tts-cache")
	reportPath := filepath.Join(paiDir, "MEMORY", "OBSERVABILITY", "tts-pronunciation-reports.jsonl")

	h := &TTSHandler{
		engine:       engine,
		diskCacheDir: diskCacheDir,
		clipCache:    make(map[string][]byte),
		subsPath:     subsPath,
		reportPath:   reportPath,
	}
	h.subs.map_ = make(map[string]string)

	return h
}

// getSubstitutions returns the current substitution map, reloading if the file changed.
func (h *TTSHandler) getSubstitutions() (map[string]string, int64) {
	h.subs.mu.Lock()
	defer h.subs.mu.Unlock()

	// Check if file has changed
	info, err := os.Stat(h.subsPath)
	mtime := int64(0)
	if err == nil {
		mtime = info.ModTime().UnixMilli()
	}

	if mtime != h.subs.mtime {
		// File changed or doesn't exist
		h.subs.mtime = mtime
		h.subs.map_ = make(map[string]string)

		if err == nil {
			// Try to read the file
			data, err := os.ReadFile(h.subsPath)
			if err == nil {
				var m map[string]string
				if err := json.Unmarshal(data, &m); err == nil {
					h.subs.map_ = m
				}
			}
		}
	}

	// Return a copy to avoid concurrent map access
	result := make(map[string]string)
	for k, v := range h.subs.map_ {
		result[k] = v
	}
	return result, h.subs.mtime
}

// normalizeForSpeech applies built-in speech normalization.
// Converts version strings like "v3.7.1" to "v 3 point 7 point 1"
func normalizeForSpeech(text string) string {
	// Handle three-part version strings (v3.7.1)
	re1 := regexp.MustCompile(`\bv(\d+)\.(\d+)\.(\d+)\b`)
	text = re1.ReplaceAllString(text, `v $1 point $2 point $3`)

	// Handle three-part without 'v' prefix (3.7.1)
	re2 := regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)
	text = re2.ReplaceAllString(text, `$1 point $2 point $3`)

	// Handle two-part version strings (v2.0)
	re3 := regexp.MustCompile(`\bv(\d+)\.(\d+)\b`)
	text = re3.ReplaceAllString(text, `v $1 point $2`)

	// Handle two-part without 'v' prefix, but avoid matching if it was already replaced
	// or if it's part of an IP address
	re4 := regexp.MustCompile(`\b(\d+)\.(\d+)\b`)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		// Skip if line contains digits followed by . followed by digits three times (IP)
		if !strings.Contains(line, " point ") && !regexp.MustCompile(`\d+\.\d+\.\d+`).MatchString(line) {
			line = re4.ReplaceAllString(line, `$1 point $2`)
		}
		lines[i] = line
	}
	text = strings.Join(lines, "\n")

	return text
}

// applySubstitutions applies pronunciation substitutions to text.
func (h *TTSHandler) applySubstitutions(text string) (string, int64) {
	subs, version := h.getSubstitutions()
	out := normalizeForSpeech(text)

	for from, to := range subs {
		// Use word boundary matching for substitutions
		pattern := fmt.Sprintf(`\b%s\b`, regexp.QuoteMeta(from))
		re := regexp.MustCompile("(?i)" + pattern)
		out = re.ReplaceAllString(out, to)
	}

	return out, version
}

// diskKey generates a cache key for disk storage.
func diskKey(key string) string {
	h := sha1.Sum([]byte(key))
	return hex.EncodeToString(h[:])[:24] + ".wav"
}

// diskGet retrieves a clip from disk cache, updating its mtime for LRU.
func (h *TTSHandler) diskGet(key string) []byte {
	path := filepath.Join(h.diskCacheDir, diskKey(key))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	// Touch mtime for LRU
	now := time.Now()
	_ = os.Chtimes(path, now, now)

	return data
}

// diskPut stores a clip on disk and prunes old entries.
func (h *TTSHandler) diskPut(key string, wav []byte) {
	if err := os.MkdirAll(h.diskCacheDir, 0755); err != nil {
		return // Best effort
	}

	path := filepath.Join(h.diskCacheDir, diskKey(key))
	if err := os.WriteFile(path, wav, 0644); err != nil {
		return // Best effort
	}

	// Prune old entries
	entries, err := os.ReadDir(h.diskCacheDir)
	if err != nil {
		return
	}

	type entry struct {
		name  string
		mtime int64
	}
	var files []entry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".wav") {
			info, _ := e.Info()
			if info != nil {
				files = append(files, entry{e.Name(), info.ModTime().UnixMilli()})
			}
		}
	}

	// Sort by mtime descending (newest first)
	for i := 0; i < len(files); i++ {
		for j := i + 1; j < len(files); j++ {
			if files[j].mtime > files[i].mtime {
				files[i], files[j] = files[j], files[i]
			}
		}
	}

	// Remove oldest files beyond the limit
	for i := diskCacheMax; i < len(files); i++ {
		_ = os.Remove(filepath.Join(h.diskCacheDir, files[i].name))
	}
}

// currentAccount returns the current user's account name.
func currentAccount() string {
	if u, _ := user.Current(); u != nil {
		return u.Username
	}
	return ""
}

// HandleTTSRequest handles the main TTS synthesis endpoint and related routes.
func (h *TTSHandler) HandleTTSRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Handle tts-correction (update substitutions)
	if r.Method == http.MethodPost && r.URL.Path == "/api/chat/tts-correction" {
		h.handleTTSCorrection(w, r)
		return
	}

	// Handle tts-report (mispronunciation flag)
	if r.Method == http.MethodPost && r.URL.Path == "/api/chat/tts-report" {
		h.handleTTSReport(w, r)
		return
	}

	// Handle main TTS synthesis
	if r.Method == http.MethodPost && r.URL.Path == "/api/chat/tts" {
		h.handleTTSSynth(w, r)
		return
	}
}

// handleTTSSynth handles POST /api/chat/tts - main synthesis endpoint.
func (h *TTSHandler) handleTTSSynth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "audio/wav")

	var req struct {
		Text  string  `json:"text"`
		Voice string  `json:"voice,omitempty"`
		Speed float64 `json:"speed,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "bad request"})
		return
	}

	text := strings.TrimSpace(req.Text)
	if len(text) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "text required"})
		return
	}

	if len(text) > textMaxLen {
		text = text[:textMaxLen]
	}

	// Apply substitutions
	synth, version := h.applySubstitutions(text)

	// Build cache key
	key := fmt.Sprintf("%s|%f|%d|%s", req.Voice, req.Speed, version, synth)

	// Check memory cache
	h.clipMutex.RLock()
	wav, ok := h.clipCache[key]
	h.clipMutex.RUnlock()

	if ok {
		w.Write(wav)
		return
	}

	// Check disk cache
	wav = h.diskGet(key)
	if wav != nil {
		h.clipMutex.Lock()
		h.clipCache[key] = wav
		if len(h.clipCache) > clipCacheMax {
			// Remove oldest entry (first key)
			for k := range h.clipCache {
				delete(h.clipCache, k)
				break
			}
		}
		h.clipMutex.Unlock()
		w.Write(wav)
		return
	}

	// Synthesize via daemon
	wavBytes, _, _, err := h.engine.Synth(synth, req.Voice, req.Speed)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Cache the result
	h.diskPut(key, wavBytes)
	h.clipMutex.Lock()
	h.clipCache[key] = wavBytes
	if len(h.clipCache) > clipCacheMax {
		// Remove oldest entry
		for k := range h.clipCache {
			delete(h.clipCache, k)
			break
		}
	}
	h.clipMutex.Unlock()

	w.Write(wavBytes)
}

// handleTTSCorrection handles POST /api/chat/tts-correction - update substitutions.
func (h *TTSHandler) handleTTSCorrection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From     string `json:"from"`
		To       string `json:"to"`
		Sentence string `json:"sentence,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "bad request"})
		return
	}

	from := strings.TrimSpace(req.From)
	to := strings.TrimSpace(req.To)

	if from == "" || to == "" {
		json.NewEncoder(w).Encode(map[string]string{"error": "from and to required"})
		return
	}

	if len(from) > subMaxLen || len(to) > subMaxLen {
		json.NewEncoder(w).Encode(map[string]string{"error": "from/to too long"})
		return
	}

	// Update substitutions
	h.subs.mu.Lock()
	h.subs.map_[from] = to
	h.subs.mtime = time.Now().UnixMilli()
	h.subs.mu.Unlock()

	// Write to file
	h.subs.mu.RLock()
	mapCopy := make(map[string]string)
	for k, v := range h.subs.map_ {
		mapCopy[k] = v
	}
	h.subs.mu.RUnlock()

	data, _ := json.MarshalIndent(mapCopy, "", "  ")
	if err := os.WriteFile(h.subsPath, append(data, '\n'), 0644); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "write failed"})
		return
	}

	// Log the correction
	entry := map[string]interface{}{
		"ts":       time.Now().Format(time.RFC3339),
		"sentence": req.Sentence,
		"voice":    "parlay-pool",
		"clipMeta": map[string]string{
			"kind": "correction",
			"from": from,
			"to":   to,
		},
	}
	if data, err := json.Marshal(entry); err == nil {
		if f, err := os.OpenFile(h.reportPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			fmt.Fprintln(f, string(data))
			f.Close()
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "substitutions": len(mapCopy)})
}

// handleTTSReport handles POST /api/chat/tts-report - flag mispronunciations.
func (h *TTSHandler) handleTTSReport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sentence string      `json:"sentence"`
		Voice    string      `json:"voice,omitempty"`
		ClipMeta interface{} `json:"clipMeta,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "bad request"})
		return
	}

	sentence := strings.TrimSpace(req.Sentence)
	if sentence == "" {
		json.NewEncoder(w).Encode(map[string]string{"error": "sentence required"})
		return
	}

	if len(sentence) > reportMaxLen {
		sentence = sentence[:reportMaxLen]
	}

	if req.Voice == "" {
		req.Voice = "parlay-pool"
	}

	entry := map[string]interface{}{
		"ts":       time.Now().Format(time.RFC3339),
		"sentence": sentence,
		"voice":    req.Voice,
	}
	if req.ClipMeta != nil {
		entry["clipMeta"] = req.ClipMeta
	}

	if data, err := json.Marshal(entry); err == nil {
		if f, err := os.OpenFile(h.reportPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			fmt.Fprintln(f, string(data))
			f.Close()
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// HandleTTSEventRequest handles POST /api/chat/tts-event - TTS event stream.
func HandleTTSEventRequest(w http.ResponseWriter, r *http.Request, hub *Hub) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost || r.URL.Path != "/api/chat/tts-event" {
		return
	}

	var msg map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "bad request"})
		return
	}

	// Add timestamp if not present
	if _, ok := msg["ts"]; !ok {
		msg["ts"] = time.Now().Format(time.RFC3339)
	}

	// Broadcast to SSE clients - TTS is device-level, not channel-scoped
	hub.broadcast("tts_event", msg)

	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// HandleTTSValidateRequest handles POST /api/chat/tts/validate-splits - TTS validation.
func HandleTTSValidateRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost || r.URL.Path != "/api/chat/tts/validate-splits" {
		return
	}

	var req struct {
		Text  string `json:"text"`
		Model string `json:"model,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "bad request"})
		return
	}

	text := strings.TrimSpace(req.Text)
	if text == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "text required"})
		return
	}

	model := req.Model
	if model == "" {
		model = "gemma4:latest"
	}

	// For now, return a placeholder response. Full Ollama integration can be added later.
	blocks := splitBlocksRaw(text)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"blocks": blocks,
		"evaluation": map[string]interface{}{
			"overall_score": 0,
			"verdict":       "unknown",
			"issues":        []interface{}{},
			"suggestion":    "Ollama integration pending",
		},
		"model": model,
		"ms":    0,
	})
}

// splitBlocksRaw mirrors the TS splitBlocksRaw function for speech splitting.
func splitBlocksRaw(text string) []map[string]string {
	// Split on sentence boundaries
	re := regexp.MustCompile(`[^.!?\n]+[.!?]*\s*`)
	parts := re.FindAllString(text, -1)
	if len(parts) == 0 {
		parts = []string{text}
	}

	var blocks []map[string]string
	var cur string

	for _, p := range parts {
		cur += p
		if len(strings.TrimSpace(cur)) >= 60 {
			blocks = append(blocks, map[string]string{
				"synth": strings.TrimSpace(cur),
				"raw":   cur,
			})
			cur = ""
		}
	}

	if len(strings.TrimSpace(cur)) > 0 {
		blocks = append(blocks, map[string]string{
			"synth": strings.TrimSpace(cur),
			"raw":   cur,
		})
	}

	if len(blocks) == 0 {
		blocks = []map[string]string{{
			"synth": strings.TrimSpace(text),
			"raw":   text,
		}}
	}

	return blocks
}
