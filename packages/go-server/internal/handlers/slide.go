// Phone slide trigger (task-l6wjg.4): a one-button page and an authed route
// that hand a tap to the SAME action-mode path the remote-input submit
// route uses, so a tap on the phone and the spoken "slide next" both end in
// actions.user.slide_next() / slide_previous() inside Talon.
//
//	GET  /slide           the page (static HTML, no secrets, no API calls)
//	POST /api/chat/slide  {"direction":"next"|"previous"} + bearer token
//
// Auth: the route fails closed. With PARLAY_SLIDE_TOKEN unset it answers 503
// and nothing runs; with it set, every POST must carry the matching bearer
// token (constant-time compare). The token is the second layer behind
// network reachability — the server binds loopback and the tailnet forward
// is tailnet-only — so a Funnel or LAN mistake still cannot move slides.
package handlers

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"parlay/go-server/internal/remoteinput"
)

// SlideTokenEnv names the env var holding the phone trigger's bearer token.
const SlideTokenEnv = "PARLAY_SLIDE_TOKEN"

// slideDevice is the device id phone-triggered submissions are recorded under.
const slideDevice = "phone-slide"

//go:embed slide.html
var slidePage []byte

// slideActions maps the wire direction to the allow-listed action name that
// remoteinput.ActionSnippet resolves — the same names voice maps to.
var slideActions = map[string]string{
	"next":     "slide_next",
	"previous": "slide_previous",
}

func handleSlidePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(slidePage)
}

// handleSlideTrigger implements POST /api/chat/slide. token is read per
// request through tokenFn so tests (and an operator rotating the env on
// restart) never race a cached value.
func handleSlideTrigger(svc *remoteinput.Service, seam remoteInputSeam, tokenFn func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		want := tokenFn()
		if want == "" {
			writeStatusError(w, http.StatusServiceUnavailable,
				"slide trigger disabled: set "+SlideTokenEnv+" and restart")
			return
		}
		got, hasPrefix := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !hasPrefix || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeStatusError(w, http.StatusUnauthorized, "bad or missing bearer token")
			return
		}
		var req struct {
			Direction string `json:"direction"`
			DryRun    bool   `json:"dryRun"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
			writeStatusError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		action, ok := slideActions[req.Direction]
		if !ok {
			writeStatusError(w, http.StatusBadRequest, `direction must be "next" or "previous"`)
			return
		}
		acceptRemoteInput(w, svc, seam, remoteinput.Submission{
			Device: slideDevice, Mode: remoteinput.ModeAction, Action: action,
			Trigger: "phone-slide", DryRun: req.DryRun,
		})
	}
}

func slideTokenFromEnv() string { return os.Getenv(SlideTokenEnv) }
