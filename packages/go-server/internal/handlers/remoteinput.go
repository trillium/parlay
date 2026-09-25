// Remote-input HTTP surface (task-57ltl): Parlay's accepted-input intake
// for the Mac-side injection control plane. Two routes:
//
//	POST /api/chat/remote-input/submit  enqueue text → 202 {id, queued}
//	GET  /api/chat/remote-input/status?id=…  poll one Outcome (404 unknown)
//
// Terminal outcomes additionally fan out as the device-scoped SSE event
// `remote_input_result` carrying the Outcome — that event is what lets
// Parlay clear its shared input state only on confirmed injection
// (status "injected") and preserve + strip the trigger on "focus_failed".
// Both routes are mutating/identifier-aiming and live in GuardedPaths.
package handlers

import (
	"net/http"

	"parlay/go-server/internal/remoteinput"
)

// remoteInputEvent is the SSE name carrying settled Outcomes.
const remoteInputEvent = "remote_input_result"

// registerRemoteInput wires the intake routes. The service drives the live
// Talon REPL; tests build handlers directly with a fake-backed service.
func registerRemoteInput(mux *http.ServeMux, hub *Hub) {
	svc := remoteinput.NewService(
		remoteinput.NewREPLTalon(),
		remoteinput.DefaultSettleDelay,
		func(o remoteinput.Outcome) {
			if o.Terminal() {
				hub.broadcastToDevice(o.Device, remoteInputEvent, o)
			}
		},
	)
	mux.HandleFunc("/api/chat/remote-input/submit", handleRemoteInputSubmit(svc))
	mux.HandleFunc("/api/chat/remote-input/status", handleRemoteInputStatus(svc))
}

// handleRemoteInputSubmit implements POST /api/chat/remote-input/submit.
func handleRemoteInputSubmit(svc *remoteinput.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var req remoteinput.SubmitRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Device == "" {
			writeStatusError(w, http.StatusBadRequest, "device is required")
			return
		}
		if req.Text == "" {
			writeStatusError(w, http.StatusBadRequest, "text is required")
			return
		}
		id := svc.Submit(remoteinput.Submission{
			Device: req.Device, Text: req.Text,
			App: req.App, WindowTitle: req.WindowTitle, Trigger: req.Trigger,
		})
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, remoteinput.SubmitResponse{ID: id, Status: remoteinput.StatusQueued})
	}
}

// handleRemoteInputStatus implements GET /api/chat/remote-input/status.
func handleRemoteInputStatus(svc *remoteinput.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			writeStatusError(w, http.StatusBadRequest, "id is required")
			return
		}
		o, ok := svc.Get(id)
		if !ok {
			writeStatusError(w, http.StatusNotFound, "unknown submission id")
			return
		}
		writeJSON(w, o)
	}
}
