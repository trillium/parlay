// Remote-input HTTP surface (task-57ltl, targets + no-target rule task-46ys9):
// Parlay's accepted-input intake for the Mac-side injection control plane.
// Five routes:
//
//	POST /api/chat/remote-input/submit  enqueue text → 202 {id, queued}
//	GET  /api/chat/remote-input/status?id=…  poll one Outcome (404 unknown)
//	GET  /api/chat/remote-input/targets      list Talon's apps in Talon order
//	POST /api/chat/remote-input/parse-words  words → resolved command (READ-ONLY)
//	POST /api/chat/remote-input/launch       start an app, focus it once → 200 {status}
//
// Terminal outcomes additionally fan out as the device-scoped SSE event
// `remote_input_result` carrying the Outcome — that event is what lets
// Parlay clear its shared input state only on confirmed injection
// (status "injected") and preserve + strip the trigger on "focus_failed".
// All five routes are mutating/identifier-aiming and live in GuardedPaths.
// Targets is read-only (no focus change, no keystroke); ?dryRun=1 on it
// is accepted as a no-op so a client can prove the read path explicitly.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"parlay/go-server/internal/inputlog"
	"parlay/go-server/internal/remoteinput"
	"parlay/go-server/internal/store"
)

// remoteInputEvent is the SSE name carrying settled Outcomes.
const remoteInputEvent = "remote_input_result"

// registerRemoteInput wires the intake routes. The service drives the live
// Talon REPL; tests build handlers directly with a fake-backed service.
//
// st is the input-seam ledger. Recording is wired here, at the service's
// accept hook and its settle callback, so every submission is recorded no
// matter which handler path accepted it.
func registerRemoteInput(mux *http.ServeMux, hub *Hub, st *store.Store) {
	seam := remoteInputSeam{st: st}
	svc := remoteinput.NewService(
		remoteinput.NewREPLTalon(),
		remoteinput.DefaultSettleDelay,
		func(o remoteinput.Outcome) {
			if o.Terminal() {
				hub.broadcastToDevice(o.Device, remoteInputEvent, o)
				seam.settled(o)
			}
		},
	)
	svc.SetOnAccepted(seam.intake)
	mux.HandleFunc("/api/chat/remote-input/submit", requireTailnetAuth(slideTokenFromEnv, handleRemoteInputSubmit(svc, st)))
	mux.HandleFunc("/api/chat/remote-input/status", requireTailnetAuth(slideTokenFromEnv, handleRemoteInputStatus(svc)))
	mux.HandleFunc("/api/chat/remote-input/targets", requireTailnetAuth(slideTokenFromEnv, handleRemoteInputTargets(svc)))
	mux.HandleFunc("/api/chat/remote-input/parse-words",
		requireTailnetAuth(slideTokenFromEnv, handleRemoteInputParseWords(svc, grammarVersionFromEnv)))
	mux.HandleFunc("/api/chat/remote-input/launch", requireTailnetAuth(slideTokenFromEnv, handleRemoteInputLaunch(svc)))
	mux.HandleFunc("/slide", handleSlidePage)
	mux.HandleFunc(slideManifestPath, handleSlideManifest)
	mux.HandleFunc(slideWorkerPath, handleSlideWorker)
	mux.HandleFunc(slideIcon180Path, handleSlideIcon180)
	mux.HandleFunc(slideIcon192Path, handleSlideIcon192)
	mux.HandleFunc(slideIcon512Path, handleSlideIcon512)
	mux.HandleFunc("/api/chat/slide", handleSlideTrigger(svc, seam, slideTokenFromEnv))
}

// handleRemoteInputSubmit implements POST /api/chat/remote-input/submit.
// Mode selects the pipeline: empty/inject types via Talon (the
// no-target rule applies); bead captures the text as a bead with no
// target needed and nothing typed; action runs one allow-listed Talon
// slide action (slide_next / slide_previous) and types nothing.
//
// Every refusal below is recorded in the input ledger before the 4xx is
// written. That is the point: a submission the intake declined previously
// left no trace anywhere, so "the phone never sent it" and "the server
// refused it" were the same silence.
func handleRemoteInputSubmit(svc *remoteinput.Service, st *store.Store) http.HandlerFunc {
	seam := remoteInputSeam{st: st}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		// Ledger key for input that never gets a submission id. An accepted
		// submission is keyed by its own ri-N id instead, so the operator can
		// replay the exact id the phone polls.
		inputID := inputlog.NewInputID()
		var req remoteinput.SubmitRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Device == "" {
			seam.refused(inputID, reasonMissingDevice)
			writeStatusError(w, http.StatusBadRequest, "device is required")
			return
		}
		// ?dryRun=true forces a dry run without touching the body:
		// real focus + real verification, nothing typed. Action mode
		// dry runs call nothing and report the snippet in wouldRun.
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
		// Action mode runs one allow-listed Talon action. It types
		// nothing, so an empty transcript is not a recogniser error
		// and no focus target is required. The name is checked here
		// before anything is queued.
		if mode == remoteinput.ModeAction {
			action := req.Action
			if q := r.URL.Query().Get("action"); q != "" && req.Action == "" {
				action = q
			}
			if _, ok := remoteinput.ActionSnippet(action); !ok {
				seam.refused(inputID, reasonActionNotAllowed)
				if action == "" {
					writeStatusError(w, http.StatusBadRequest,
						`action is required: want "slide_next" or "slide_previous"`)
					return
				}
				writeStatusError(w, http.StatusBadRequest,
					fmt.Sprintf(`action %q not allowed: want "slide_next" or "slide_previous"`, action))
				return
			}
			acceptRemoteInput(w, svc, seam, remoteinput.Submission{
				Device: req.Device, Text: req.Text, Mode: mode, Action: action,
				Trigger: req.Trigger, DryRun: dryRun, Confidence: req.Confidence,
			})
			return
		}
		if req.Text == "" {
			// Nothing was transcribed. That is a recogniser error, not a
			// missing message: the input arrived and became nothing.
			seam.recogniserError(inputID)
			writeStatusError(w, http.StatusBadRequest, "text is required")
			return
		}
		if mode != remoteinput.ModeInject && mode != remoteinput.ModeBead {
			seam.refused(inputID, reasonUnknownMode)
			writeStatusError(w, http.StatusBadRequest,
				`unknown mode: want "inject", "bead", or "action"`)
			return
		}
		store := remoteinput.NormalizeStore(req.Store)
		if q := r.URL.Query().Get("store"); q != "" && req.Store == "" {
			store = remoteinput.NormalizeStore(q)
		}
		if mode == remoteinput.ModeBead {
			if !remoteinput.ValidStore(store) {
				seam.refused(inputID, reasonInvalidStore)
				writeStatusError(w, http.StatusBadRequest,
					`invalid bead store: must match ^[a-z][a-z0-9_-]{0,63}$ (any registered wrapper name works)`)
				return
			}
			if n := len([]rune(req.Text)); n > remoteinput.MaxBeadTextLen {
				seam.refused(inputID, reasonBeadTextTooLong)
				writeStatusError(w, http.StatusBadRequest,
					"bead text exceeds 2000 chars: rejected, never truncated")
				return
			}
			// Bead mode needs no target and types nothing.
			acceptRemoteInput(w, svc, seam, remoteinput.Submission{
				Device: req.Device, Text: req.Text, Mode: mode, Store: store,
				Trigger: req.Trigger, DryRun: dryRun, Confidence: req.Confidence,
			})
			return
		}
		// No silent blind injection: a live inject submit with no app
		// and no window target must name the unfocused mode
		// deliberately. (Dry runs type nothing, so they stay exempt.)
		if !dryRun && req.App == "" && req.WindowTitle == "" && !allowUnfocused {
			seam.refused(inputID, reasonNoTarget)
			writeStatusError(w, http.StatusBadRequest,
				"no target: set app or windowTitle (see GET remote-input/targets for Talon names), "+
					"or send allowUnfocused:true to inject without focus")
			return
		}
		acceptRemoteInput(w, svc, seam, remoteinput.Submission{
			Device: req.Device, Text: req.Text,
			App: req.App, WindowTitle: req.WindowTitle, Trigger: req.Trigger,
			Mode: mode, AllowUnfocused: allowUnfocused, DryRun: dryRun,
			Confidence: req.Confidence,
		})
	}
}

// acceptRemoteInput applies the confidence threshold and then either holds
// the submission or queues it. A hold is terminal and types nothing: the
// submission never reaches the service's queue, and the held outcome is what
// tells the phone its text is preserved because a threshold stopped it.
//
// The threshold is only ever compared against a confidence the caller
// actually reported. Nothing is held on an absent confidence — see
// inputlog.Judge for why that asymmetry is deliberate rather than a gap.
func acceptRemoteInput(w http.ResponseWriter, svc *remoteinput.Service, seam remoteInputSeam, sub remoteinput.Submission) {
	if min := seam.minConfidence(); min != nil {
		if v := inputlog.Judge(min, sub.Confidence); v.Hold {
			sub.ID = svc.Hold(sub, fmt.Sprintf(
				"confidence %.2f is below the configured minimum %.2f: held, nothing was typed",
				*sub.Confidence, *min))
			seam.intake(sub)
			seam.held(sub)
			w.WriteHeader(http.StatusAccepted)
			writeJSON(w, remoteinput.SubmitResponse{ID: sub.ID, Status: remoteinput.StatusHeld})
			return
		}
	}
	id := svc.Submit(sub)
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, remoteinput.SubmitResponse{ID: id, Status: remoteinput.StatusQueued})
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

// GrammarVersionEnv names the env var holding the desktop's grammar version.
// Unset, the stale_grammar check is off (no export exists to version yet).
const GrammarVersionEnv = "PARLAY_TALON_GRAMMAR_VERSION"

// grammarVersionFromEnv reads the desktop grammar version per request, so an
// operator bumping it needs no restart of the reader.
func grammarVersionFromEnv() string { return os.Getenv(GrammarVersionEnv) }

// handleRemoteInputParseWords implements POST /api/chat/remote-input/parse-words
// (task-z0caa Stage 1). READ-ONLY: it asks Talon what the words resolve to
// and executes nothing: no mimic, no insert, no key, no focus. Auth is not
// here: the registration wraps it in requireTailnetAuth, the one gate every
// remote-input route shares.
//
// Domain outcomes (matched, no_match, parse_mismatch, stale_grammar,
// talon_asleep) are all 200 with an `outcome`; only transport problems are
// non-2xx. A sleeping Talon is its own outcome and carries `modes`, so it
// cannot be mistaken for a grammar miss.
func handleRemoteInputParseWords(svc *remoteinput.Service, versionFn remoteinput.GrammarVersionFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var req remoteinput.ParseWordsRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
			writeStatusError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		req.Words = remoteinput.NormalizeWords(req.Words)
		if req.Words == "" {
			writeStatusError(w, http.StatusBadRequest, "words is required")
			return
		}
		if n := len([]rune(req.Words)); n > remoteinput.MaxParseWordsLen {
			writeStatusError(w, http.StatusBadRequest,
				fmt.Sprintf("words exceeds %d chars: rejected, never truncated", remoteinput.MaxParseWordsLen))
			return
		}
		resp, err := svc.ParseWords(req, versionFn)
		if err != nil {
			writeStatusError(w, http.StatusBadGateway, "talon parse failed: "+err.Error())
			return
		}
		writeJSON(w, resp)
	}
}

// handleRemoteInputLaunch implements POST /api/chat/remote-input/launch
// (task-2cp06), the PWA's launchable-apps row. Unlike submit it is
// synchronous: 200 with the outcome status (launched, focused,
// dry_run_passed), 400 for a missing device/app, 404 for an app GET targets
// does not list, 502 when Talon cannot be read, 500 when the launch fails.
// It types nothing; focus failure after a successful launch is still 200.
func handleRemoteInputLaunch(svc *remoteinput.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var req remoteinput.LaunchRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Device == "" {
			writeStatusError(w, http.StatusBadRequest, "device is required")
			return
		}
		if req.App == "" {
			writeStatusError(w, http.StatusBadRequest, "app is required")
			return
		}
		if q := r.URL.Query().Get("dryRun"); q == "true" || q == "1" {
			req.DryRun = true
		}
		resp, err := svc.Launch(req)
		switch {
		case errors.Is(err, remoteinput.ErrUnknownApp):
			writeStatusError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, remoteinput.ErrLaunchFailed):
			writeStatusError(w, http.StatusInternalServerError, err.Error())
		case err != nil:
			writeStatusError(w, http.StatusBadGateway, err.Error())
		default:
			writeJSON(w, resp)
		}
	}
}
