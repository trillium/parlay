// Guarded Talon-control routes (task-w8nwu): the allow-list gate in front of
// anything that makes Talon act on the laptop. Two SEPARATE paths, each with
// its own enable flag (both default off) and the same caller gate:
//
//	POST /api/chat/talon/run     {"command":"slide_next","dryRun":false}
//	POST /api/chat/talon/inject  {"text":"…","app":"Keynote","dryRun":false}
//
// Order of refusal, so an unauthenticated caller learns nothing about config:
// origin (403) → token (503 unset / 401 bad) → path enabled (403) → body
// (400/403 for anything off the allow-list). Policy lives in
// remoteinput.Guardrails; see its header for why widening the default
// allow-list needs captain review.
package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"parlay/go-server/internal/inputlog"
	"parlay/go-server/internal/remoteinput"
)

const (
	talonRunPath    = "/api/chat/talon/run"
	talonInjectPath = "/api/chat/talon/inject"

	// talonDevice records guarded submissions in the outcome store/ledger.
	talonDevice = "talon-control"

	// maxTalonInjectText bounds one injection (runes); over-long text is
	// rejected, never truncated.
	maxTalonInjectText = 4000

	reasonTalonCommandNotAllowed = "talon-command-not-allowed"
	reasonTalonAppNotAllowed     = "talon-app-not-allowed"
)

// talonGuardrailsFromEnv resolves the policy at startup. A bad config fails
// closed to the refuse-everything policy and says why.
func talonGuardrailsFromEnv() remoteinput.Guardrails {
	g, err := remoteinput.GuardrailsFromEnv(os.LookupEnv)
	if err != nil {
		log.Printf("talon guardrails: %v — talon-control paths disabled", err)
	}
	return g
}

// talonGate runs the caller checks shared by both paths. It writes the
// refusal and returns false when the request must stop.
func talonGate(w http.ResponseWriter, r *http.Request, g remoteinput.Guardrails, enabled bool, flag string) bool {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return false
	}
	if !remoteinput.TailnetOrigin(r.RemoteAddr) {
		writeStatusError(w, http.StatusForbidden, "origin not permitted: tailnet or loopback only")
		return false
	}
	if !g.TokenConfigured() {
		writeStatusError(w, http.StatusServiceUnavailable,
			"talon control disabled: set "+remoteinput.TalonTokenEnv+" and restart")
		return false
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !g.TokenValid(got) {
		writeStatusError(w, http.StatusUnauthorized, "bad or missing bearer token")
		return false
	}
	if !enabled {
		writeStatusError(w, http.StatusForbidden, "this path is disabled: set "+flag+"=1 to enable")
		return false
	}
	return true
}

// decodeStrict rejects unknown fields so a request cannot smuggle a field a
// future change might start honouring.
func decodeStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeStatusError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// handleTalonRun implements POST /api/chat/talon/run.
func handleTalonRun(svc *remoteinput.Service, seam remoteInputSeam, g remoteinput.Guardrails) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !talonGate(w, r, g, g.RunEnabled, remoteinput.TalonRunEnv) {
			return
		}
		var req struct {
			Command string `json:"command"`
			DryRun  bool   `json:"dryRun"`
		}
		if !decodeStrict(w, r, &req) {
			return
		}
		if !g.CommandAllowed(req.Command) {
			seam.refused(inputlog.NewInputID(), reasonTalonCommandNotAllowed)
			writeStatusError(w, http.StatusForbidden, fmt.Sprintf(
				"command %q not allowed: want one of %s", req.Command, strings.Join(g.AllowedCommands(), ", ")))
			return
		}
		acceptRemoteInput(w, svc, seam, remoteinput.Submission{
			Device: talonDevice, Mode: remoteinput.ModeAction, Action: req.Command,
			Trigger: "talon-run", DryRun: req.DryRun,
		})
	}
}

// handleTalonInject implements POST /api/chat/talon/inject. The target app
// is mandatory and must be allow-listed: there is no unfocused injection.
func handleTalonInject(svc *remoteinput.Service, seam remoteInputSeam, g remoteinput.Guardrails) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !talonGate(w, r, g, g.InjectEnabled, remoteinput.TalonInjectEnv) {
			return
		}
		var req struct {
			Text   string `json:"text"`
			App    string `json:"app"`
			DryRun bool   `json:"dryRun"`
		}
		if !decodeStrict(w, r, &req) {
			return
		}
		if !g.AppAllowed(req.App) {
			seam.refused(inputlog.NewInputID(), reasonTalonAppNotAllowed)
			writeStatusError(w, http.StatusForbidden, fmt.Sprintf("app %q not allowed", req.App))
			return
		}
		if req.Text == "" {
			writeStatusError(w, http.StatusBadRequest, "text is required")
			return
		}
		if len([]rune(req.Text)) > maxTalonInjectText {
			writeStatusError(w, http.StatusBadRequest, "text exceeds 4000 chars: rejected, never truncated")
			return
		}
		acceptRemoteInput(w, svc, seam, remoteinput.Submission{
			Device: talonDevice, Text: req.Text, App: req.App,
			Mode: remoteinput.ModeInject, Trigger: "talon-inject", DryRun: req.DryRun,
		})
	}
}
