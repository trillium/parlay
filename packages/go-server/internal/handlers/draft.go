package handlers

import (
	"net/http"

	"parlay/go-server/internal/store"
)

type putDraftRequest struct {
	Text     string `json:"text"`
	ClientID string `json:"clientId"`
}

// handleDraft implements GET/PUT /api/chat/draft on one mux registration
// (matching the rest of this package's one-pattern-per-path convention,
// since net/http.ServeMux panics on registering the same exact pattern
// twice). GET's response includes the whole stored store.Draft — the
// contract only documents callers reading `text`, but clientId/updatedAt
// are harmless extra fields. PUT's response is documented as unconsumed by
// its one known caller but is returned anyway for consistency with every
// other write endpoint in this package.
func handleDraft(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, st.Drafts.Get())
		case http.MethodPut:
			var req putDraftRequest
			if !decodeJSON(w, r, &req) {
				return
			}
			saved, err := st.Drafts.Set(req.Text, req.ClientID)
			if err != nil {
				writeStatusError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, saved)
		default:
			methodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
		}
	}
}
