// Package guard is the Go port of packages/server/src/guard/ — the one
// security boundary for the unauthenticated chat API (task-6ai1, defect D7 of
// the end-to-end verification). Over there the policy lives in
// guard/origin.ts + guard/index.ts and the route set in guard/paths.ts; this
// package holds both.
//
// Before this package, packages/go-server had no origin boundary of any kind.
// An end-to-end verifier sent cross-origin CORS *simple requests* (Content-
// Type: text/plain, no preflight, no network position) from
// https://evil.example and got 200 from /send, /alert, /register-agent and
// /unregister: text landed in a live agent's stream, an agent named
// "evil-agent" was created, and a live agent was removed. The Bun server
// refused all four with 403. This package closes that gap.
//
// It is DELIBERATELY not an authentication scheme. Both servers are
// unauthenticated by design and documented as such; this is only the
// cross-origin half, exactly as the TS guard is.
//
// # Semantics, and where they differ from packages/server/src/guard/
//
// Identical:
//
//   - A request with NO Origin header is ALLOWED. That is the CLI, curl,
//     hooks and every server-to-server caller, and a browser cannot forge
//     that absence on a cross-site request. This is what keeps the live fleet
//     working and is the single most important rule here.
//   - A request WITH an Origin must be same-origin (Origin's host:port equals
//     the request's Host), or a loopback / .local / private-LAN literal, or
//     listed in PARLAY_ALLOWED_ORIGINS. Anything else is 403 with no CORS
//     headers at all, so the calling page cannot read the outcome either.
//   - Origin "null" (sandboxed iframe, file://, some redirects) is refused.
//   - PARLAY_ALLOWED_ORIGINS is a comma-separated list of exact origins; the
//     single value "*" opts out of the origin check entirely.
//   - Guarded POST/PUT must carry Content-Type: application/json, else 415.
//     This is what stops a cross-origin simple request from reaching a
//     handler without a preflight — and preflight on a guarded path from a
//     disallowed origin is refused. /api/chat/upload is exempt (multipart by
//     contract); there, the origin check alone is the defense.
//   - A guarded response never carries a wildcard Access-Control-Allow-Origin:
//     it reflects the single allowed origin, plus Vary: Origin.
//
// Two deliberate divergences, both in the direction of LESS access:
//
//  1. Unguarded routes here send no Access-Control-Allow-Origin at all,
//     where the TS guard still spreads a wildcard `CORS` on its read/SSE
//     routes. This server has never sent CORS headers on any route, so adding
//     a wildcard to match the TS side would newly OPEN read access that is
//     currently closed. Cross-origin reads of /history, /agents and /events
//     therefore still execute (as they do on the TS side) but their bodies
//     remain unreadable to a foreign page. If the panel is ever served from a
//     different origin than this server, that is the knob to revisit.
//  2. OPTIONS on an unguarded route is left to the route's own handler
//     (today: 405), where the TS guard answers a blanket 204 + wildcard. Same
//     reasoning — no preflight permission this server does not already grant.
//
// One divergence that is not a policy choice: the guarded path SETS differ
// because the two servers do not implement the same routes. Go has
// /api/chat/message (TS does not); TS has /system, /declare-channel, /clear,
// /navigate, /reload, /device-cmd, /eval, /eval-push, the /tts family, the
// /plugin/ RPC prefix, /api/debug/ and DELETE /api/chat/agents/:id, none of
// which exist here. Every route the two DO share is classified identically.
//
// # How a route gets into GuardedPaths
//
// THE RULE, the same one stated in packages/server/src/guard/paths.ts: a
// route is guarded if it mutates server state or discloses an identifier,
// REGARDLESS OF HTTP METHOD. The verb is not evidence — /subscribers and
// /poll are both GETs and both guarded, the first because it hands out the
// device uuid and every agent id, the second because polling registers the
// channel.
//
// Apply that rule to THIS server's handlers; do not copy the TS set. The two
// implementations of a shared route can differ in what they touch, and
// handlePoll is the worked example: the TS handler auto-registers an unknown
// channel in the agent registry, broadcasts agent_register and persists to
// disk, while this one only takes a Presence poller slot for the life of the
// request and never writes the registry. Both are guarded — a poller entry is
// still server state a foreign page must not be able to create, and keeping
// the two sets aligned on shared routes is worth more than the narrowest
// possible boundary — but the classification was derived here, not inherited.
package guard

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// GuardedPaths is every route on this server that mutates state or discloses
// an identifier a cross-origin page could then aim at one of the mutating
// routes — see "How a route gets into GuardedPaths" in the package comment
// for the rule, which is method-independent. Mirrors GUARDED_CHAT_PATHS in
// packages/server/src/guard/paths.ts for the routes the two servers share.
//
// A new mutating route is UNGUARDED until it is added here. If its callers do
// not send a JSON content type, it also belongs in jsonExemptPaths.
var GuardedPaths = map[string]bool{
	// D7, the routes the verifier drove cross-origin.
	"/api/chat/send":           true,
	"/api/chat/alert":          true,
	"/api/chat/register-agent": true,
	"/api/chat/unregister":     true,

	// The rest of this server's write surface, same class.
	"/api/chat/reply":           true,
	"/api/chat/message":         true,
	"/api/chat/draft":           true, // PUT writes it; GET reads the captain's in-progress text
	"/api/chat/parlay/settings": true, // PUT rewrites persisted panel/voice settings
	"/api/chat/upload":          true, // origin check only — see jsonExemptPaths

	// Read-only, but it is the route that handed the TS-side attack chain its
	// connected device uuid and the ids of every registered agent (D9).
	"/api/chat/subscribers": true,

	// A GET that takes a Presence poller slot for the life of the request,
	// which /subscribers then reports. Guarded on this server's own behavior,
	// not by copying TS — see the package comment's handlePoll asymmetry. No
	// caller is affected: every poller in this repo is a no-Origin HTTP
	// client, and nothing in packages/client polls.
	"/api/chat/poll": true,
}

// jsonExemptPaths are guarded paths that must NOT be held to
// Content-Type: application/json. /api/chat/upload is multipart/form-data by
// contract, so the content-type gate would reject every legitimate upload.
// The origin check alone is sufficient there: a browser always sends Origin
// on a cross-origin request, including a multipart form POST.
var jsonExemptPaths = map[string]bool{
	"/api/chat/upload": true,
}

// IsGuarded reports whether path is inside the guard.
func IsGuarded(path string) bool { return GuardedPaths[path] }

// privateV4 mirrors guard/origin.ts's PRIVATE_V4 exactly: loopback and private-LAN
// literals. The phone reaches the panel over the LAN and a reverse proxy may
// rewrite Host, so a strict same-host test alone would cut off legitimate
// local clients. None of these can be an attacker's origin without them
// already serving pages from inside the captain's network, and DNS rebinding
// does not help — the Origin header keeps the attacker's own name.
var privateV4 = regexp.MustCompile(`^(10\.|127\.|169\.254\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)`)

func isLocalHostname(hostname string) bool {
	h := strings.ToLower(strings.Trim(hostname, "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if strings.HasSuffix(h, ".local") {
		return true
	}
	if h == "::1" || h == "0:0:0:0:0:0:0:1" {
		return true
	}
	return privateV4.MatchString(h)
}

// AllowedOriginList reads PARLAY_ALLOWED_ORIGINS — comma-separated exact
// origins (e.g. a tunnel hostname). "*" opts out of the origin check
// entirely: an escape hatch for a deployment that needs it, never the
// default.
func AllowedOriginList() []string {
	raw := os.Getenv("PARLAY_ALLOWED_ORIGINS")
	if raw == "" {
		return nil
	}
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// OriginAllowed is the port of guard/origin.ts's originAllowed.
func OriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	// No Origin at all → not a browser cross-site request. CLI, curl, hooks,
	// the relay. Allowing this is what keeps the live fleet working.
	if origin == "" {
		return true
	}

	for _, a := range AllowedOriginList() {
		if a == "*" || a == origin {
			return true
		}
	}

	// "null" is what a sandboxed iframe / file:// / redirected request sends.
	if origin == "null" {
		return false
	}

	scheme, hostport, ok := splitOrigin(origin)
	if !ok || (scheme != "http" && scheme != "https") {
		return false
	}

	// Same-origin: the Origin's host:port matches the Host this request
	// arrived on. Covers localhost:31337, the LAN IP, and any tunnel that
	// forwards Host.
	if r.Host != "" && strings.EqualFold(hostport, r.Host) {
		return true
	}

	return isLocalHostname(hostnameOf(hostport))
}

// splitOrigin parses "scheme://host[:port]" without url.Parse's tolerance for
// paths, queries and userinfo — an Origin has none of those, and accepting
// them would let "https://evil.example/@localhost" style values through a
// looser parser.
func splitOrigin(origin string) (scheme, hostport string, ok bool) {
	i := strings.Index(origin, "://")
	if i <= 0 {
		return "", "", false
	}
	scheme = strings.ToLower(origin[:i])
	hostport = origin[i+3:]
	if hostport == "" || strings.ContainsAny(hostport, "/?#@") {
		return "", "", false
	}
	return scheme, hostport, true
}

// hostnameOf strips the port from a host:port, leaving IPv6 brackets for
// isLocalHostname to trim (it does the same for the TS side's URL.hostname).
func hostnameOf(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// IsJSONContentType is the port of guard/origin.ts's isJsonContentType.
func IsJSONContentType(value string) bool {
	if value == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(strings.Split(value, ";")[0]), "application/json")
}

// deny writes a rejection carrying NO Access-Control-Allow-Origin — the
// calling page must not be able to read the outcome either.
func deny(w http.ResponseWriter, status int, msg string) {
	w.Header().Del("Access-Control-Allow-Origin")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Vary", "Origin")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// setGuardedCORS puts the reflected-origin CORS headers on a guarded
// response. Never a wildcard: reflect the single allowed origin so a
// same-origin panel can still read its own responses, and Vary so a shared
// cache cannot hand one origin's ACAO to another.
func setGuardedCORS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Vary", "Origin")
	origin := r.Header.Get("Origin")
	if origin == "" || !OriginAllowed(r) {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// Wrap returns next with the origin/content-type guard in front of it. Apply
// it once, to the whole mux, so no route can be added outside the boundary —
// which route is guarded is then decided by GuardedPaths alone.
func Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !IsGuarded(path) {
			// Unguarded (read/SSE) routes: unchanged behavior, and
			// deliberately still no CORS headers — see the package comment.
			next.ServeHTTP(w, r)
			return
		}

		if !OriginAllowed(r) {
			if r.Method == http.MethodOptions {
				deny(w, http.StatusForbidden, "cross-origin preflight rejected")
				return
			}
			deny(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}

		// Preflight on a guarded path from an ALLOWED origin: answer it here
		// rather than letting it fall through to a handler that would 405.
		if r.Method == http.MethodOptions {
			setGuardedCORS(w, r)
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Only POST/PUT can arrive without a preflight, so only they need the
		// content-type gate; GET carries no body and DELETE is never a CORS
		// simple request.
		if (r.Method == http.MethodPost || r.Method == http.MethodPut) &&
			!jsonExemptPaths[path] && !IsJSONContentType(r.Header.Get("Content-Type")) {
			deny(w, http.StatusUnsupportedMediaType, "Content-Type: application/json required")
			return
		}

		setGuardedCORS(w, r)
		next.ServeHTTP(w, r)
	})
}
