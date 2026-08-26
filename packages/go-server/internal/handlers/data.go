package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"parlay/go-server/internal/store"
)

// RegisterData wires ticket C3's routes onto mux: drafts, uploads, and
// settings — a self-contained data/CRUD surface with its own Register-style
// entry point (kept separate from C1's Register in handlers.go) so this
// ticket never has to touch, or depend on, C1's messaging/registry broker or
// a later C2's SSE hub. Call alongside Register and registerHealth in
// cmd/parlay-server/main.go.
func RegisterData(mux *http.ServeMux, st *store.Store) {
	mux.HandleFunc("/api/chat/draft", handleDraft(st))

	mux.HandleFunc("/api/chat/upload", handleUpload(st))
	mux.HandleFunc(uploadURLPrefix, handleServeUpload(st))

	mux.HandleFunc("/api/chat/parlay/settings", handleSettings(st))
}

// RegisterTTS wires the TTS route family onto mux: synthesis, events,
// corrections, reports, and validation. Requires PAI_DIR and a Hub for
// event broadcasting.
func RegisterTTS(mux *http.ServeMux, paiDir string, hub *Hub) {
	// Initialize TTS engine and handler
	socketPath := getTTSSocketPath()
	engine := NewSpeakDaemonEngine(socketPath, 30*time.Second)
	handler := NewTTSHandler(engine, paiDir)

	// Register TTS synthesis and correction endpoints
	ttsWrapper := func(w http.ResponseWriter, r *http.Request) {
		handler.HandleTTSRequest(w, r)
	}
	mux.HandleFunc("/api/chat/tts", ttsWrapper)
	mux.HandleFunc("/api/chat/tts-correction", ttsWrapper)
	mux.HandleFunc("/api/chat/tts-report", ttsWrapper)

	// Register TTS event endpoint
	mux.HandleFunc("/api/chat/tts-event", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/chat/tts-event" {
			http.NotFound(w, r)
			return
		}
		HandleTTSEventRequest(w, r, hub)
	})

	// Register TTS validation endpoint
	mux.HandleFunc("/api/chat/tts/validate-splits", HandleTTSValidateRequest)
}

// getTTSSocketPath returns the speak daemon socket path, accounting for the current user.
func getTTSSocketPath() string {
	account := currentAccount()
	if account == "" {
		account = "unknown"
	}
	return filepath.Join(os.TempDir(), "speak-"+account+".sock")
}
