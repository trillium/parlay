package evalengine

import (
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
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
//   - Otherwise only the captain's own network: localhost, *.localhost,
//     *.local, loopback, private-LAN v4, Tailscale (100.64.0.0/10, *.ts.net).
//
// Deliberately a small local port, not an import: the engine is a stdlib-only
// static binary and the guard package lives in a different module tree.

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
	if h, port, err := net.SplitHostPort(hostport); err == nil {
		// SplitHostPort accepts any port text; an Origin port is digits.
		if _, perr := strconv.ParseUint(port, 10, 16); perr != nil {
			return hostport
		}
		return h
	}
	return hostport
}

// isEvalLocalHostname reports whether hostname is one of the captain's own network
// locations: localhost, *.localhost, *.local (mDNS), IPv6 loopback, a v4
// literal in loopback / link-local / RFC1918 private-LAN space, a Tailscale
// CGNAT address (100.64.0.0/10), a *.ts.net MagicDNS name, a bare single-label
// hostname, or an IPv6 loopback / ULA (fc00::/7, incl. the tailnet
// fd7a:115c:a1e0::/48) / link-local (fe80::/10) address.
//
// v4 is matched on the PARSED address, never a string prefix: "10.evil.com"
// and "192.168.1.1.evil.com" are public names, not LAN addresses. ".ts.net" is
// a suffix match on a label boundary, so "evil-ts.net" and "x.ts.net.evil.com"
// do not qualify.
func isEvalLocalHostname(hostname string) bool {
	h := strings.ToLower(strings.Trim(hostname, "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".ts.net") {
		return true
	}
	ip, err := netip.ParseAddr(h)
	if err != nil {
		return isSingleLabelHost(h)
	}
	if ip.Is6() {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || evalTailnetV4.Contains(ip)
}

var evalTailnetV4 = netip.MustParsePrefix("100.64.0.0/10")

// isSingleLabelHost matches a bare, dot-less hostname such as a Tailscale
// MagicDNS short name or a LAN machine name ("macbook", "mini1"). Public names
// always contain a dot, so a single label cannot be an attacker's domain. All-
// digit labels are refused: browsers read "http://10" as an IPv4 literal.
func isSingleLabelHost(h string) bool {
	if h == "" || len(h) > 63 || h[0] == '-' || h[len(h)-1] == '-' {
		return false
	}
	letter := false
	for _, c := range h {
		switch {
		case c >= 'a' && c <= 'z':
			letter = true
		case c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return letter
}
