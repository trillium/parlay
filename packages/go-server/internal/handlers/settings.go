package handlers

import (
	"net/http"

	"parlay/go-server/internal/store"
)

// handleSettings implements GET/PUT /api/chat/parlay/settings on one mux
// registration (see handleDraft's doc comment for why GET and PUT share a
// single handler in this package). PUT is a whole-document replace per the
// contract, not a patch: any field the request body omits is stored as its
// Go zero value, matching SettingsStore.Replace's own documented semantics.
// The legacy voiceClearPhrase migration only applies to documents already on
// disk (SettingsStore's own load path) — the contract's migration note is
// about the client normalizing that shape on load, so a PUT body is expected
// to already be in the current shape by the time it reaches this handler.
func handleSettings(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, st.Settings.Get())
		case http.MethodPut:
			var req store.ParlaySettings
			if !decodeJSON(w, r, &req) {
				return
			}
			saved, err := st.Settings.Replace(req)
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
