// Tailnet gate for the remote-input routes (task-hyo0c).
//
// The control plane has no authentication of its own: it relies on being
// reachable only from this Mac. Exposing submit/status/targets through
// `tailscale serve` (see bin/parlay-tailnet-serve) makes them reachable from
// the phone, so a request that did NOT originate locally must now prove
// itself the same way /api/chat/slide does:
//
//   - a loopback peer carrying Tailscale-User-Login (set by `tailscale serve`
//     after authenticating the tailnet user) passes, unless
//     PARLAY_TRUST_TAILSCALE_HEADER=0; or
//   - the request carries the bearer token (PARLAY_SLIDE_TOKEN, else the
//     pairing token); or
//   - it is refused, 503 when no token exists at all.
//
// "Did not originate locally" means a non-loopback peer, or any proxy marker
// (X-Forwarded-*, Forwarded, Tailscale-*). A plain loopback request with none
// of those — the Parlay panel, the voice pipeline, curl on this Mac — is
// untouched, so existing local callers keep working with no token. A tailnet
// device without a user identity (a tagged node) still reaches us through the
// proxy, carries X-Forwarded-For, and therefore needs the token.
package handlers

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// proxyMarkerHeaders are set by a reverse proxy (tailscale serve sets
// X-Forwarded-For and, for user devices, Tailscale-User-Login).
var proxyMarkerHeaders = []string{
	"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded",
	"Tailscale-User-Login", "Tailscale-User-Name",
}

// locallyOriginated reports a loopback peer with no proxy marker.
func locallyOriginated(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return false
	}
	for _, h := range proxyMarkerHeaders {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	return true
}

// requireTailnetAuth wraps a remote-input handler with the gate above.
// tokenFn is read per request, like handleSlideTrigger's.
func requireTailnetAuth(tokenFn func() string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if locallyOriginated(r) || tailscaleIdentified(r) {
			next(w, r)
			return
		}
		want := tokenFn()
		if want == "" {
			writeStatusError(w, http.StatusServiceUnavailable,
				"remote-input over the tailnet disabled: no pairing token; set "+SlideTokenEnv+" and restart")
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeStatusError(w, http.StatusUnauthorized, "bad or missing bearer token")
			return
		}
		next(w, r)
	}
}
