package handlers

import (
	"net/http"

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
