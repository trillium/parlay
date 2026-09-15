package evalengine

import (
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// Origin gate for POST /eval (phase-2 voice identity, task-9nldh).
//
// The eval hot path used to accept any POST with a streamId: any page able
// to reach the engine could drive evaluations. This ports the go-server
// guard's OriginAllowed shape (packages/go-server/internal/guard/guard.go)
// so the two halves of the voice loop enforce the same rule:
//
//   - No Origin header → not a browser cross-site request. The go-server
//     relay, curl, and hooks send none; all pass through.
//   - PARLAY_EVAL_ALLOWED_ORIGINS → comma-separated exact origins (a tunnel
//     hostname, the tailnet page). "*" opts out entirely, never the default.
//   - "null" (sandboxed iframe / file://) is always rejected.
//   - Same-origin (Origin host:port == request Host) is allowed: covers
//     localhost:4343, the LAN IP, and any tunnel that forwards Host.
//   - Otherwise only local hostnames (localhost, *.localhost, *.local,
//     loopback, private-LAN v4) — the captain's own pages.
//
// Deliberately a small local port, not an import: the engine is a stdlib-only
// static binary and the guard package lives in a different module tree.

// evalPrivateV4 mirrors the go-server guard's PRIVATE_V4: loopback and
// private-LAN ranges.
var evalPrivateV4 = regexp.MustCompile(`^(10\.|127\.|169\.254\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)`)

// evalAllowedOriginList reads PARLAY_EVAL_ALLOWED_ORIGINS — comma-separated
// exact origins. "*" opts out of the origin check entirely.
func evalAllowedOriginList() []string {
	raw := os.Getenv("PARLAY_EVAL_ALLOWED_ORIGINS")
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

// evalOriginAllowed reports whether r's Origin may POST to /eval.
func evalOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	for _, a := range evalAllowedOriginList() {
		if a == "*" || a == origin {
			return true
		}
	}

	if origin == "null" {
		return false
	}

	scheme, hostport, ok := splitEvalOrigin(origin)
	if !ok || (scheme != "http" && scheme != "https") {
		return false
	}

	if r.Host != "" && strings.EqualFold(hostport, r.Host) {
		return true
	}

	return isEvalLocalHostname(hostnameOfEval(hostport))
}

// splitEvalOrigin parses "scheme://host[:port]" without url.Parse's
// tolerance for paths, queries and userinfo — an Origin has none of those.
func splitEvalOrigin(origin string) (scheme, hostport string, ok bool) {
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

// hostnameOfEval strips the port from a host:port.
func hostnameOfEval(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// isEvalLocalHostname accepts the captain's own hostnames: localhost,
// .local (mDNS), loopback, and private-LAN v4.
func isEvalLocalHostname(hostname string) bool {
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
	return evalPrivateV4.MatchString(h)
}
