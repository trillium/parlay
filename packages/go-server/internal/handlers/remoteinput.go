// Remote-input HTTP surface (task-57ltl, targets + no-target rule task-46ys9):
// Parlay's accepted-input intake for the Mac-side injection control plane.
// Three routes:
//
//	POST /api/chat/remote-input/submit  enqueue text → 202 {id, queued}
//	GET  /api/chat/remote-input/status?id=…  poll one Outcome (404 unknown)
//	GET  /api/chat/remote-input/targets      list Talon's apps in Talon order
//
// Terminal outcomes additionally fan out as the device-scoped SSE event
// `remote_input_result` carrying the Outcome — that event is what lets
// Parlay clear its shared input state only on confirmed injection
// (status "injected") and preserve + strip the trigger on "focus_failed".
// All three routes are mutating/identifier-aiming and live in GuardedPaths.
// Targets is read-only (no focus change, no keystroke); ?dryRun=1 on it
// is accepted as a no-op so a client can prove the read path explicitly.
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
	mux.HandleFunc("/api/chat/remote-input/targets", handleRemoteInputTargets(svc))
}

// handleRemoteInputSubmit implements POST /api/chat/remote-input/submit.
// Mode selects the pipeline: empty/inject types via Talon (the
// no-target rule applies); bead captures the text as a bead with no
// target needed and nothing typed.
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
		// ?dryRun=true forces a dry run without touching the body:
		// real focus + real verification, nothing typed.
		dryRun := req.DryRun
		if q := r.URL.Query().Get("dryRun"); q == "true" || q == "1" {
			dryRun = true
		}
		// ?allowUnfocused=1 opts into targetless injection without
		// touching the body, mirroring dryRun above.
		allowUnfocused := req.AllowUnfocused
		if q := r.URL.Query().Get("allowUnfocused"); q == "true" || q == "1" {
			allowUnfocused = true
		}
		// Mode + store: query params mirror the body fields (?mode=bead
		// ?store=task); the body wins when both are set.
		mode := remoteinput.NormalizeMode(req.Mode)
		if q := r.URL.Query().Get("mode"); q != "" && req.Mode == "" {
			mode = remoteinput.NormalizeMode(q)
		}
		if mode != remoteinput.ModeInject && mode != remoteinput.ModeBead {
			writeStatusError(w, http.StatusBadRequest,
				`unknown mode: want "inject" or "bead"`)
			return
		}
		store := remoteinput.NormalizeStore(req.Store)
		if q := r.URL.Query().Get("store"); q != "" && req.Store == "" {
			store = remoteinput.NormalizeStore(q)
		}
		if mode == remoteinput.ModeBead {
			if !remoteinput.ValidStore(store) {
				writeStatusError(w, http.StatusBadRequest,
					`invalid bead store: must match ^[a-z][a-z0-9_-]{0,63}$ (any registered wrapper name works)`)
				return
			}
			if n := len([]rune(req.Text)); n > remoteinput.MaxBeadTextLen {
				writeStatusError(w, http.StatusBadRequest,
					"bead text exceeds 2000 chars: rejected, never truncated")
				return
			}
			// Bead mode needs no target and types nothing.
			id := svc.Submit(remoteinput.Submission{
				Device: req.Device, Text: req.Text, Mode: mode, Store: store,
				Trigger: req.Trigger, DryRun: dryRun,
			})
			w.WriteHeader(http.StatusAccepted)
			writeJSON(w, remoteinput.SubmitResponse{ID: id, Status: remoteinput.StatusQueued})
			return
		}
		// No silent blind injection: a live inject submit with no app
		// and no window target must name the unfocused mode
		// deliberately. (Dry runs type nothing, so they stay exempt.)
		if !dryRun && req.App == "" && req.WindowTitle == "" && !allowUnfocused {
			writeStatusError(w, http.StatusBadRequest,
				"no target: set app or windowTitle (see GET remote-input/targets for Talon names), "+
					"or send allowUnfocused:true to inject without focus")
			return
		}
		id := svc.Submit(remoteinput.Submission{
			Device: req.Device, Text: req.Text,
			App: req.App, WindowTitle: req.WindowTitle, Trigger: req.Trigger,
			Mode: mode, AllowUnfocused: allowUnfocused, DryRun: dryRun,
		})
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, remoteinput.SubmitResponse{ID: id, Status: remoteinput.StatusQueued})
	}
}

// handleRemoteInputTargets implements GET /api/chat/remote-input/targets.
// One bounded Talon read per call; read-only, so ?dryRun=1 is a no-op
// echoed back for callers that want the mode visible.
func handleRemoteInputTargets(svc *remoteinput.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		targets, err := svc.Targets()
		if err != nil {
			writeStatusError(w, http.StatusBadGateway, "talon targets failed: "+err.Error())
			return
		}
		if targets == nil {
			targets = []remoteinput.Target{}
		}
		resp := remoteinput.TargetsResponse{Targets: targets}
		if q := r.URL.Query().Get("dryRun"); q == "true" || q == "1" {
			resp.DryRun = true
		}
		writeJSON(w, resp)
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
