// Package guard is the Go port of packages/server/src/guard/ — the one
// security boundary for the unauthenticated chat API (task-6ai1, defect D7 of
// the end-to-end verification). In the deleted TypeScript server the policy lived
// in guard/origin.ts + guard/index.ts and the route set in guard/paths.ts; this
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
//     the request's Host), or a loopback / .local / RFC1918 private-LAN /
//     Tailscale (100.64.0.0/10, *.ts.net) host, or listed in PARLAY_ALLOWED_ORIGINS. Anything else is 403 with no CORS
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
//     currently closed. Cross-origin reads of /history and /agents therefore
//     still execute (as they do on the TS side) but their bodies remain
//     unreadable to a foreign page. If the panel is ever served from a
//     different origin than this server, that is the knob to revisit. The same
//     reasoning is why noGuardedCORSReads exists: guarding a path also turns
//     ACAO ON for the origins the guard allows, and a read that has never
//     sent one must not gain it as a side effect of its own path acquiring a
//     mutating method. The one deliberate exception is GET /api/chat/events,
//     which reflects ACAO for origins listed verbatim in
//     PARLAY_ALLOWED_ORIGINS (listedOriginCORSReads) so the herdr web page can
//     read its stream.
//  2. OPTIONS on an unguarded route is left to the route's own handler
//     (today: 405), where the TS guard answers a blanket 204 + wildcard. Same
//     reasoning — no preflight permission this server does not already grant.
//
// One divergence that is not a policy choice: the guarded path SETS differ
// where the two servers do not implement the same routes. Go has
// /api/chat/message (TS does not); TS has /api/debug/input-timing, which does
// not exist here (the /api/debug/ prefix below pre-lands its boundary
// anyway). Every route the two DO share is classified identically — except
// the exact path /api/chat/agents, whose deliberate asymmetry is explained at
// the /api/chat/agents/ prefix entry.
//
// # How a route gets into GuardedPaths
//
// THE RULE, the same one stated in packages/server/src/guard/paths.ts:
// GuardedPaths is the mutating and identifier-aiming surface, and within it
// membership is decided by what the handler DOES, REGARDLESS OF HTTP METHOD.
// The verb is not evidence — /subscribers and /poll are both GETs and both
// guarded, the first because it hands out the device uuid and every agent id,
// the second because polling registers the channel.
//
// As on the TS side, that is a description of the boundary that exists, not a
// claim that nothing outside it writes or discloses. TS carries two routes of
// known, accepted, deliberately-unguarded residue (GET /api/chat/events,
// which stores an attacker-supplied ?device= in its SSE client record and
// streams tts_event frames carrying that uuid to every client, and GET
// /api/chat/agents, which returns every registered agent id) — tracked there
// as identifier-disclosure-remains-on-sse. /api/chat/events is not part of
// this server's residue: it is guarded here, because POST on that path is the
// external-producer ingress into the SSE hub and the classification rule is
// method-independent — and its GET stream reflects ACAO only to origins the
// operator listed in PARLAY_ALLOWED_ORIGINS (listedOriginCORSReads).
// This server's residue is smaller
// for two reasons, neither of them a route-set decision: divergence 1 above
// means its unguarded routes send no ACAO at all, so a foreign page's read
// still executes but its body stays unreadable, and handleEvents accepts
// ?device= without storing it (see internal/handlers/events.go). Neither is a
// guarantee about what those handlers do — only about what a browser will
// hand back — so classify a new read route here on its own behavior.
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
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// GuardedPaths is this server's mutating and identifier-aiming surface: the
// routes that write state, or hand out an identifier a cross-origin page could
// then aim at one of the mutating routes. See "How a route gets into
// GuardedPaths" in the package comment for the classification test, which is
// method-independent, and for the residue that test does not cover — TS carries
// two accepted-residue read routes, and this server's exposure differs because
// divergence 1 means its unguarded routes send no ACAO at all. Mirrored
// GUARDED_CHAT_PATHS in packages/server/src/guard/paths.ts (deleted with the
// TS server in the Bun→Go cutover) for the routes the two servers shared.
//
// A new mutating route is UNGUARDED until it is added here. If its callers do
// not send a JSON content type, it also belongs in jsonExemptPaths.
//
// That rule is ENFORCED, not just documented: TestEveryRegisteredRouteIsGuardedOrExplained
// parses internal/handlers for every path it puts on the mux and fails the
// build on one that is neither guarded here nor listed, with a written reason,
// in TestUnguardedRoutes. The live-command registry's three report routes
// shipped outside the boundary before that test existed; see their entries.
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

	// POST pushes an event to every connected SSE client (the external-producer
	// ingress in internal/handlers/events_ingress.go); guarded on that, and the
	// method-independent rule then covers the GET stream on the same path too.
	// On the refusal side that is stricter than the TS server, where GET
	// /api/chat/events is accepted residue (identifier-disclosure-remains-on-sse),
	// and no caller notices: the panel is same-origin, and every other caller
	// (the TS tailers, the CLI, curl) sends no Origin. It is not stricter in
	// every direction, though — guarding a path is also what makes this server
	// reflect an ACAO to the origins it DOES allow. The GET stream narrows that
	// to origins listed in PARLAY_ALLOWED_ORIGINS; see listedOriginCORSReads.
	"/api/chat/events": true,

	// A GET that takes a Presence poller slot for the life of the request,
	// which /subscribers then reports. Guarded on this server's own behavior,
	// not by copying TS — see the package comment's handlePoll asymmetry. No
	// caller is affected: every poller in this repo is a no-Origin HTTP
	// client, and nothing in packages/client polls.
	"/api/chat/poll": true,

	// Pre-landed for the routes still being ported from packages/server. Each
	// one is classified here on what its TS handler DOES — which is the same
	// thing its Go port will do, since these are parity ports — so that the
	// port lands inside the boundary instead of outside it. The rule this
	// file exists to enforce ("a new mutating route is UNGUARDED until it is
	// added here") fails in exactly one predictable way: a route added by
	// someone editing internal/handlers and never opening this file. Landing
	// the entries first removes that window, and an entry for a path no
	// handler serves yet costs nothing — IsGuarded is consulted per request,
	// so an unrouted path 404s before the guard's decision matters.
	//
	// Re-classify, do not trust this comment, if a Go handler ends up doing
	// something its TS counterpart does not — that asymmetry is real and
	// handlePoll above is the worked example.
	"/api/chat/clear":                 true, // wipes persisted chat history
	"/api/chat/system":                true, // injects a system message into the thread
	"/api/chat/navigate":              true, // drives the panel to a URL
	"/api/chat/reload":                true, // force-reloads every connected panel
	"/api/chat/device-cmd":            true, // drives the connected device
	"/api/chat/declare-channel":       true, // mutates the agent/channel registry
	"/api/chat/eval":                  true, // relays code to the eval engine
	"/api/chat/eval-push":             true, // pushes an eval result back
	"/api/chat/plugin/cursorless/rpc": true, // plugin RPC — origin check only, see jsonExemptPaths
	"/api/chat/tts":                   true, // synthesizes and speaks on the captain's device
	"/api/chat/tts-report":            true, // appends to the pronunciation report file
	"/api/chat/tts-correction":        true, // rewrites persisted pronunciation substitutions
	"/api/chat/tts/validate-splits":   true, // origin check only, see jsonExemptPaths
	"/api/chat/tts-event":             true, // broadcasts a tts_event frame carrying a device uuid
	"/api/chat/debug-log":             true, // appends client console errors to a log file on disk

	// Remote-input intake (task-57ltl; targets + no-target rule task-46ys9):
	// POST enqueues accepted text for injection as real keystrokes on the
	// target Mac via Talon; GET polls the per-submission outcome; GET
	// targets lists Talon's apps in Talon's ordering (read-only).
	// Mutating/identifier-aiming by the file's own rule — what the handler
	// DOES, regardless of method — so all three land here. JSON bodies,
	// so no jsonExemptPaths entry.
	"/api/chat/remote-input/submit":  true,
	"/api/chat/remote-input/status":  true,
	"/api/chat/remote-input/targets": true,

	// Live-command registry (#91): three POST report routes the CLI calls to
	// announce a running verb. Mutating by the file's own rule — each writes
	// a registry row that GET /api/chat/commands and the panel's live-commands
	// view then report — so all three land here.
	//
	// They were added OUTSIDE this map when the feature shipped, carrying a
	// hand-rolled copy of this guard's content-type gate in the handler
	// (handlers.requireCommandReport) on the belief, stated in that function's
	// comment, that "this server has no equivalent guard". That was true when
	// the guard landed (task-6ai1) and false by the time the registry did, and
	// the consequence was a route that accepted a forged cross-origin POST
	// with Content-Type: application/json — a shape the handler's own gate
	// cannot refuse, because requiring JSON is exactly what forces the
	// preflight the handler assumed nothing would ever answer. Two
	// implementations of one boundary is the defect class this file exists to
	// prevent, so the routes come inside it and requireCommandReport stays as
	// defense in depth.
	//
	// GET /api/chat/commands is the read half and deliberately stays OUT, on
	// the /api/chat/agents precedent: a foreign page's read executes but its
	// body is unreadable, because unguarded routes here send no ACAO at all.
	"/api/chat/command-start":     true,
	"/api/chat/command-heartbeat": true,
	"/api/chat/command-end":       true,

	// The command log and the off switch (sandbox task). /off-switch is
	// mutating on POST — it turns a connection or an action off, or back on — so
	// the method-independent rule puts the GET on the same path inside the
	// boundary too. /action-log is a GET that hands out device ids, command ids
	// and per-action verbs: identifier-disclosure by the same test that guards
	// /subscribers, and it is in noGuardedCORSReads below so guarding it does not
	// newly reflect an ACAO on a body that has never sent one.
	//
	// Neither route is an authorization layer and neither reimplements a guard
	// check: the switch can only ever SUBTRACT delivery from work this server was
	// already willing to do, and a request the guard refuses never reaches it.
	"/api/chat/action-log": true,
	"/api/chat/off-switch": true,
}

// jsonExemptPaths are guarded paths that must NOT be held to
// Content-Type: application/json. /api/chat/upload is multipart/form-data by
// contract, so the content-type gate would reject every legitimate upload.
// The origin check alone is sufficient there: a browser always sends Origin
// on a cross-origin request, including a multipart form POST.
//
// The other two members are pre-landed for the in-flight parity ports of
// /api/chat/plugin/cursorless/rpc and /api/chat/tts/validate-splits; the
// reasoning is at the entries themselves. Classify anything further on its own
// handler and its own callers, exactly as with GuardedPaths — never by copying
// the TS list.
var jsonExemptPaths = map[string]bool{
	"/api/chat/upload": true,

	// Pre-landed alongside their GuardedPaths entries above, on the same
	// caller evidence TS decided them on and NOT by copying the TS list: the
	// cursorless RPC's only caller is an out-of-repo Talon script whose JSON
	// body arrives under whatever content type Python gave it, and
	// validate-splits has no in-repo caller at all, so its only callers are
	// hand-typed `curl -d`, which defaults to x-www-form-urlencoded. Both stay
	// INSIDE the guard — the exemption drops the content-type layer, not the
	// origin check. Adding a third member is a decision to make against the
	// same three-part test, never a per-bug-report habit; the TS comment next
	// to JSON_EXEMPT_PATHS states it in full.
	"/api/chat/plugin/cursorless/rpc": true,
	"/api/chat/tts/validate-splits":   true,
}

// guardedPrefixes are subtrees where EVERY path is guarded, including ones
// that do not exist yet. The map above is a list of routes someone remembered;
// this is a boundary that does not need remembering, and the difference
// matters most exactly where the route table grows.
//
//   - /api/chat/plugin/ — the plugin RPC subtree. internal/handlers/plugins.go
//     registers this whole prefix on the mux (makePluginRouteHandler), so every
//     plugin route a later change adds is reachable the moment it is written.
//     With exact matching only, /api/chat/plugin/cursorless/response was open:
//     a POST that deletes from rpcWaiters, stops the waiter's timer, and
//     resolves a pending RPC with an attacker-supplied `result` that is then
//     returned to the Talon caller. Guessing a 32-hex rpcId makes that a narrow
//     hole rather than a wide one, but the reachability is the defect, and the
//     next plugin route may not have an unguessable id in front of it. TS
//     guards this as a prefix and says why: "so a plugin added later is guarded
//     by default instead of shipping open until someone remembers this file."
//     Go's exact map had the opposite default.
//   - /api/chat/agents/ — DELETE /api/chat/agents/:id, the REST alias for
//     unregister. Trailing slash on purpose: it must NOT catch the exact path
//     /api/chat/agents, which is a read route and stays open here (Go sends no
//     ACAO on unguarded routes at all, so a foreign page cannot read it; TS
//     guards its own /api/chat/agents because unguarded routes THERE still
//     carry the legacy wildcard CORS. Same boundary, different defaults — that
//     divergence is correct and pinned by tests on both sides).
//   - /api/debug/ — input-timing telemetry, keyed by device id. No Go handler
//     yet (the contract marks the route ts-only), which is the same
//     land-the-entry-first discipline the map above documents: the predictable
//     way this boundary fails is a route added by someone editing
//     internal/handlers who never opens this file.
var guardedPrefixes = []string{
	"/api/chat/agents/",
	"/api/chat/plugin/",
	"/api/debug/",
}

// IsGuarded reports whether path is inside the guard: an exact member of
// GuardedPaths, or anything under a guarded prefix.
func IsGuarded(path string) bool {
	if GuardedPaths[path] {
		return true
	}
	for _, p := range guardedPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// isLocalHostname reports whether hostname is one of the captain's own network
// locations: localhost, *.localhost, *.local (mDNS), IPv6 loopback, a v4
// literal in loopback / link-local / RFC1918 private-LAN space, a Tailscale
// CGNAT address (100.64.0.0/10), or a *.ts.net MagicDNS name.
//
// v4 is matched on the PARSED address, never a string prefix: "10.evil.com"
// and "192.168.1.1.evil.com" are public names, not LAN addresses. ".ts.net" is
// a suffix match on a label boundary, so "evil-ts.net" and "x.ts.net.evil.com"
// do not qualify.
func isLocalHostname(hostname string) bool {
	h := strings.ToLower(strings.Trim(hostname, "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".ts.net") {
		return true
	}
	ip, err := netip.ParseAddr(h)
	if err != nil {
		return false
	}
	if ip.Is6() {
		return ip == netip.IPv6Loopback()
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || tailnetV4.Contains(ip)
}

var tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")

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
	// arrived on. Covers localhost:4242, the LAN IP, and any tunnel that
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
	if h, port, err := net.SplitHostPort(hostport); err == nil {
		// SplitHostPort accepts any port text; an Origin port is digits.
		if _, perr := strconv.ParseUint(port, 10, 16); perr != nil {
			return hostport
		}
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

// noGuardedCORSReads maps a guarded path to the one read method whose
// responses must NOT carry the reflected-origin CORS headers.
//
// Guarding a path does two things, not one: it refuses a disallowed origin,
// and it hands an ALLOWED one a reflected ACAO it did not have before —
// "allowed" here including every loopback, .local and private-v4 origin
// OriginAllowed accepts, i.e. any page on the captain's LAN. For a mutating
// route that trade is the point. For a READ route that only became guarded
// because a mutating method landed on its path, it can be a regression: the
// body answered the same bytes before and a foreign page simply could not read
// it, and now a LAN page can.
//
// The path stays in GuardedPaths: the 403 for a disallowed origin is the whole
// reason it is there. Only the header-setting step is skipped.
//
// /api/chat/events is NOT here any more: the herdr web page is served from
// another origin and must read that stream, so it has its own, narrower rule
// (listedOriginCORSReads below).
var noGuardedCORSReads = map[string]string{
	// /action-log is a guarded READ: the guard is there because the path's
	// sibling POST mutates and because the body carries device ids, but the GET
	// itself has never sent CORS headers and must not start doing so.
	"/api/chat/action-log": http.MethodGet,
}

// listedOriginCORSReads maps a guarded path to the one read method whose
// response reflects Access-Control-Allow-Origin only for an origin in
// streamOriginTrusted's set: named verbatim in PARLAY_ALLOWED_ORIGINS, or an
// http/https origin whose host is on the captain's own network (loopback,
// .local, RFC1918 private LAN, Tailscale 100.64.0.0/10 or *.ts.net). It is
// stricter than the reflected grant ordinary guarded routes give: an origin
// that OriginAllowed accepts only because it is same-Host (e.g. a tunnel that
// forwards Host) gets the stream and no ACAO, and the "*" entry never turns
// into a wildcard — an untrusted origin is never reflected.
//
// GET /api/chat/events is the one member: the herdr web page (http://<host>:8787,
// where host is a LAN IP, a tailnet address or name) builds its server URL as
// ${protocol}//${hostname}:4242, so its EventSource is cross-origin, and without
// an ACAO it never receives the input_action reply that makes the spoken
// "bravely" line-ender submit.
var listedOriginCORSReads = map[string]string{
	"/api/chat/events": http.MethodGet,
}

// suppressesGuardedCORS reports whether this guarded request is one of the
// read methods in noGuardedCORSReads.
func suppressesGuardedCORS(path, method string) bool {
	m, ok := noGuardedCORSReads[path]
	return ok && m == method
}

// listedOriginOnlyCORS reports whether this guarded request is one of the
// read methods in listedOriginCORSReads.
func listedOriginOnlyCORS(path, method string) bool {
	m, ok := listedOriginCORSReads[path]
	return ok && m == method
}

// streamOriginTrusted reports whether origin may read a listedOriginCORSReads
// stream: listed verbatim in PARLAY_ALLOWED_ORIGINS ("*" matches nothing), or
// an http/https origin on the captain's own network.
func streamOriginTrusted(origin string) bool {
	if origin == "" || origin == "null" {
		return false
	}
	for _, a := range AllowedOriginList() {
		if a != "*" && a == origin {
			return true
		}
	}
	scheme, hostport, ok := splitOrigin(origin)
	if !ok || (scheme != "http" && scheme != "https") {
		return false
	}
	return isLocalHostname(hostnameOf(hostport))
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

		if suppressesGuardedCORS(path, r.Method) {
			// Vary still belongs here — this response genuinely does differ by
			// Origin (403 vs. the stream), and Vary is a cache directive, not a
			// grant.
			w.Header().Set("Vary", "Origin")
		} else if listedOriginOnlyCORS(path, r.Method) {
			w.Header().Set("Vary", "Origin")
			if streamOriginTrusted(r.Header.Get("Origin")) {
				w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
			}
		} else {
			setGuardedCORS(w, r)
		}
		next.ServeHTTP(w, r)
	})
}
