package handlers

import (
	"net/http"
	"strings"
	"time"

	"parlay/go-server/internal/store"
)

// The off switch's HTTP surface: the read that reports what is off, and the
// flip that turns a connection or an action off (or back on). Split from
// actionlog.go, which owns the log those rows are read from.
//
// This is not an authorization layer and it does not reimplement the guard:
// /api/chat/off-switch is an ordinary mutating route in guard.GuardedPaths, so
// the origin and content-type gates run in front of it like every other one, and
// a request the guard refuses never reaches this store. What the switch can do
// is bounded the same way — it can only subtract delivery from work the server
// was already willing to do.
// offSwitchRequest is POST /api/chat/off-switch's body. `off` is a pointer so a
// missing field is a validation error rather than a silent "turn it on".
type offSwitchRequest struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Off     *bool  `json:"off"`
	By      string `json:"by,omitempty"`
	Surface string `json:"surface,omitempty"`
}

// offSwitchResponse reports the resulting state plus the full current off-set,
// so a caller never has to re-read to know what it accomplished. The off-set is
// `targets` here and in the action-log payload — one key, one meaning, so a
// renderer holds one shape regardless of which route it read.
type offSwitchResponse struct {
	OK      bool             `json:"ok"`
	Kind    string           `json:"kind"`
	ID      string           `json:"id"`
	Off     bool             `json:"off"`
	Changed bool             `json:"changed"`
	Entry   *store.OffEntry  `json:"entry,omitempty"`
	Targets []store.OffEntry `json:"targets"`
}

// handleOffSwitch implements GET (read the off-set) and POST (flip one target)
// on /api/chat/off-switch.
func handleOffSwitch(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, map[string]any{
				"ok":          true,
				"now":         time.Now().UTC().Format(time.RFC3339Nano),
				"targets":     st.OffSwitch.List(),
				"kinds":       store.OffKinds,
				"surfaces":    []string{"website", "cli", "api"},
				"connections": st.OffSwitch.Count(store.OffKindConnection),
				"actions":     st.OffSwitch.Count(store.OffKindAction),
			})
		case http.MethodPost:
			handleOffSwitchPost(st)(w, r)
		default:
			methodNotAllowed(w, "GET, POST")
		}
	}
}

func handleOffSwitchPost(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req offSwitchRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Off == nil {
			writeStatusError(w, http.StatusBadRequest, "off is required (true to turn it off, false to turn it back on)")
			return
		}
		entry, changed, err := st.OffSwitch.Set(
			strings.ToLower(strings.TrimSpace(req.Kind)),
			req.ID, *req.Off, req.By, store.NormalizeOffSurface(req.Surface),
		)
		if err != nil {
			writeStatusError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp := offSwitchResponse{
			OK:      true,
			Kind:    entry.Kind,
			ID:      entry.ID,
			Off:     *req.Off,
			Changed: changed,
			Targets: st.OffSwitch.List(),
		}
		if *req.Off {
			resp.Entry = &entry
		}
		writeJSON(w, resp)
	}
}
