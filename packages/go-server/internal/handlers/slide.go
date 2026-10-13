// Phone slide trigger (task-l6wjg.4): a one-button page and an authed route
// that hand a tap to the SAME action-mode path the remote-input submit
// route uses, so a tap on the phone and the spoken "slide next" both end in
// actions.user.slide_next() / slide_previous() inside Talon.
//
//	GET  /slide                         the page (static HTML, no secrets)
//	GET  /slide/manifest.webmanifest    web app manifest (static, no secret)
//	GET  /slide/sw.js                   service worker for that shell only
//	GET  /slide/icon-{180,192,512}.png  home-screen icons (static, no secret)
//	POST /api/chat/slide                {"direction":"next"|"previous"} + bearer token
//
// Auth: the route fails closed. The bearer token is PARLAY_SLIDE_TOKEN when
// set, else the pairing token the server generated on first run
// (internal/pair, <state>/pair-token) — so a fresh install needs no
// hand-made secret. With neither, it answers 503 and nothing runs; otherwise
// every POST must carry the matching bearer token (constant-time compare).
// The token is the second layer behind network reachability, so a Funnel or
// LAN mistake still cannot move slides.
//
// Tailnet exception: a loopback peer carrying a Tailscale-User-Login header
// skips the token (`tailscale serve` proxies from 127.0.0.1 and sets that
// header after authenticating the tailnet user), so the phone needs no token
// on the tailnet. The header is client-forgeable, so this assumes the server
// sits behind `tailscale serve` with Funnel off and no other local proxy or
// tunnel (cloudflared, ngrok) that could forward it; set
// PARLAY_TRUST_TAILSCALE_HEADER=0 to disable it.
package handlers

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"parlay/go-server/internal/remoteinput"
)

// SlideTokenEnv names the env var holding the phone trigger's bearer token.
const SlideTokenEnv = "PARLAY_SLIDE_TOKEN"

// TrustTailscaleHeaderEnv opts out of (=0) the Tailscale-User-Login bypass.
const TrustTailscaleHeaderEnv = "PARLAY_TRUST_TAILSCALE_HEADER"

// slideDevice is the device id phone-triggered submissions are recorded under.
const slideDevice = "phone-slide"

//go:embed slide.html
var slidePage []byte

//go:embed slideassets/manifest.webmanifest
var slideManifest []byte

//go:embed slideassets/sw.js
var slideWorker []byte

//go:embed slideassets/icon-180.png
var slideIcon180 []byte

//go:embed slideassets/icon-192.png
var slideIcon192 []byte

//go:embed slideassets/icon-512.png
var slideIcon512 []byte

// Paths are package-level constants so the route-coverage parser (which
// resolves const strings, not concatenated expressions) sees each one.
const (
	slideManifestPath = "/slide/manifest.webmanifest"
	slideWorkerPath   = "/slide/sw.js"
	slideIcon180Path  = "/slide/icon-180.png"
	slideIcon192Path  = "/slide/icon-192.png"
	slideIcon512Path  = "/slide/icon-512.png"
)

// slideShellCacheControl is storeable by the Cache API. no-store would make
// the service worker's precache of this shell fail. The bytes embed no
// secret; must-revalidate keeps a shared cache from reusing them unchecked.
const slideShellCacheControl = "private, max-age=0, must-revalidate"

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
	w.Header().Set("Cache-Control", slideShellCacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(slidePage)
}

func handleSlideManifest(w http.ResponseWriter, r *http.Request) {
	writeSlideAsset(w, r, slideManifest, "application/manifest+json; charset=utf-8")
}

func handleSlideWorker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The script lives at /slide/sw.js, so its default max scope is /slide/
	// and does not cover the page at /slide. This widens that max scope to
	// the page and the assets under it, and no further.
	w.Header().Set("Service-Worker-Allowed", "/slide")
	_, _ = w.Write(slideWorker)
}

func handleSlideIcon180(w http.ResponseWriter, r *http.Request) {
	writeSlideAsset(w, r, slideIcon180, "image/png")
}

func handleSlideIcon192(w http.ResponseWriter, r *http.Request) {
	writeSlideAsset(w, r, slideIcon192, "image/png")
}

func handleSlideIcon512(w http.ResponseWriter, r *http.Request) {
	writeSlideAsset(w, r, slideIcon512, "image/png")
}

func writeSlideAsset(w http.ResponseWriter, r *http.Request, body []byte, contentType string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", slideShellCacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
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
		if !tailscaleIdentified(r) {
			if want == "" {
				writeStatusError(w, http.StatusServiceUnavailable,
					"slide trigger disabled: no pairing token (state dir unwritable?); set "+SlideTokenEnv+" and restart")
				return
			}
			got, hasPrefix := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !hasPrefix || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
				writeStatusError(w, http.StatusUnauthorized, "bad or missing bearer token")
				return
			}
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

var (
	pairMu    sync.RWMutex
	pairToken string
)

// SetPairToken installs the server-generated pairing token the slide route
// accepts when PARLAY_SLIDE_TOKEN is unset. Called once at startup.
func SetPairToken(t string) {
	pairMu.Lock()
	defer pairMu.Unlock()
	pairToken = t
}

// slideTokenFromEnv returns the env override if set, else the pairing token.
func slideTokenFromEnv() string {
	if v := os.Getenv(SlideTokenEnv); v != "" {
		return v
	}
	pairMu.RLock()
	defer pairMu.RUnlock()
	return pairToken
}

// tailscaleIdentified reports whether the request came through a local
// `tailscale serve` proxy that vouched for the caller: a loopback peer plus
// the Tailscale-User-Login header, unless TrustTailscaleHeaderEnv is 0.
func tailscaleIdentified(r *http.Request) bool {
	if os.Getenv(TrustTailscaleHeaderEnv) == "0" {
		return false
	}
	if r.Header.Get("Tailscale-User-Login") == "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
