package guard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// req builds a request the way a browser would present it to this server:
// Host is what the request arrived on, Origin is who sent it.
func req(t *testing.T, method, path, origin, contentType string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader("{}"))
	r.Host = "localhost:4242"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return r
}

func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		// The rule the whole fleet depends on: no Origin means not a browser
		// cross-site request. CLI, curl, hooks, the relay.
		{"no Origin header at all", "", true},
		{"the panel's own origin", "http://localhost:4242", true},
		{"loopback on another port", "http://127.0.0.1:8080", true},
		{"the phone over the LAN", "http://192.168.1.42:4242", true},
		{"a bonjour .local name", "http://captain.local:4242", true},
		{"IPv6 loopback", "http://[::1]:4242", true},

		{"a hostile page", "https://evil.example.com", false},
		{"a hostile page that merely mentions localhost", "https://localhost.evil.example.com", false},
		{"a path-bearing value a loose parser would mis-split", "https://evil.example.com/@localhost", false},
		{"a sandboxed iframe / file://", "null", false},
		{"a non-http scheme", "chrome-extension://abcdef", false},
		{"a public IP", "http://203.0.113.7", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := OriginAllowed(req(t, http.MethodPost, "/api/chat/send", c.origin, "application/json")); got != c.want {
				t.Fatalf("OriginAllowed(%q) = %v, want %v", c.origin, got, c.want)
			}
		})
	}
}

// TestSameHostIsTheOnlyThingAcceptingATunnelledPanel isolates the same-host
// comparison in OriginAllowed. Every other origin in TestOriginAllowed is a
// local hostname (localhost, 127.0.0.1, 192.168.x, .local, ::1), so deleting
// that comparison leaves all of them green — these two cases are what pin it.
// The shape it exists for is a panel reached through a Host-forwarding tunnel
// or reverse proxy under a name that is none of those; if the branch silently
// regresses, that panel gets 403 on every mutating route.
func TestSameHostIsTheOnlyThingAcceptingATunnelledPanel(t *testing.T) {
	const origin = "http://panel.tunnel.test:8443"

	on := func(host string) *http.Request {
		r := req(t, http.MethodPut, "/api/chat/draft", origin, "application/json")
		r.Host = host
		return r
	}

	if !OriginAllowed(on("panel.tunnel.test:8443")) {
		t.Fatal("an origin equal to the Host it arrived on must be accepted")
	}
	// The control: without it the accept case above could be passing for some
	// other reason.
	if OriginAllowed(on("other.tunnel.test:8443")) {
		t.Fatal("the same non-local origin arriving on a different Host must be refused")
	}
}

func TestAllowedOriginsEnvOptsIn(t *testing.T) {
	t.Setenv("PARLAY_ALLOWED_ORIGINS", "https://tunnel.example.com, https://other.example.com")
	if !OriginAllowed(req(t, http.MethodPost, "/api/chat/send", "https://tunnel.example.com", "application/json")) {
		t.Fatal("an explicitly allow-listed origin must be accepted")
	}
	if OriginAllowed(req(t, http.MethodPost, "/api/chat/send", "https://evil.example.com", "application/json")) {
		t.Fatal("allow-listing one origin must not allow every origin")
	}
}

func TestAllowedOriginsWildcardIsTheDocumentedEscapeHatch(t *testing.T) {
	t.Setenv("PARLAY_ALLOWED_ORIGINS", "*")
	if !OriginAllowed(req(t, http.MethodPost, "/api/chat/send", "https://evil.example.com", "application/json")) {
		t.Fatal(`PARLAY_ALLOWED_ORIGINS="*" must opt out of the origin check entirely`)
	}
}

func TestIsJSONContentType(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"  APPLICATION/JSON ", true},
		// The three shapes a CORS *simple request* may use — the whole reason
		// this gate exists.
		{"text/plain", false},
		{"multipart/form-data; boundary=x", false},
		{"application/x-www-form-urlencoded", false},
		{"", false},
	} {
		if got := IsJSONContentType(c.in); got != c.want {
			t.Fatalf("IsJSONContentType(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestGuardedPathsCoverEveryMutatingRoute pins the route classification
// itself: the guard mechanism being correct is worthless if a mutating route
// sits outside the set, which is exactly how D7/D9 happened.
func TestGuardedPathsCoverEveryMutatingRoute(t *testing.T) {
	mustGuard := []string{
		"/api/chat/send", "/api/chat/reply", "/api/chat/alert", "/api/chat/message",
		"/api/chat/register-agent", "/api/chat/unregister",
		"/api/chat/draft", "/api/chat/upload", "/api/chat/parlay/settings",
		"/api/chat/subscribers",
		// A GET, and still guarded: the verb is not the classifier. handlePoll
		// takes a Presence poller slot that /subscribers then reports.
		"/api/chat/poll",
		// Also not classified by its verb: POST on this path pushes an event to
		// every connected SSE client (handlers.handleEventsIngress), so the
		// path is guarded and the GET stream on it comes along.
		"/api/chat/events",
	}
	for _, p := range mustGuard {
		if !IsGuarded(p) {
			t.Errorf("%s mutates state or discloses identifiers and must be guarded", p)
		}
	}
	// Read/SSE routes stay open — a guard that refuses these breaks the panel.
	for _, p := range []string{
		"/health", "/api/chat/history", "/api/chat/agents",
		"/api/chat/uploads/abc123.png",
	} {
		if IsGuarded(p) {
			t.Errorf("%s is a read route and must stay unguarded", p)
		}
	}
}

// pass is a stand-in handler: if the guard lets a request through, the
// response says so unambiguously.
func pass() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("reached the handler"))
	})
}

func TestWrapRefusesCrossOriginOnGuardedPaths(t *testing.T) {
	rec := httptest.NewRecorder()
	Wrap(pass()).ServeHTTP(rec, req(t, http.MethodPost, "/api/chat/send", "https://evil.example.com", "application/json"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "reached the handler") {
		t.Fatal("a refused request must never reach the handler")
	}
	// No CORS headers on a refusal: the calling page must not be able to read
	// the outcome either.
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q on a refusal, want none", got)
	}
}

func TestWrapRefusesTheSimpleRequestShape(t *testing.T) {
	// text/plain from an ALLOWED origin still 415s: a same-site simple request
	// is still a request that skipped preflight.
	rec := httptest.NewRecorder()
	Wrap(pass()).ServeHTTP(rec, req(t, http.MethodPost, "/api/chat/send", "http://localhost:4242", "text/plain"))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

func TestWrapAllowsTheCLIAndThePanel(t *testing.T) {
	for _, c := range []struct{ name, origin string }{
		{"CLI / curl / hooks (no Origin)", ""},
		{"the panel itself", "http://localhost:4242"},
		{"the phone on the LAN", "http://192.168.1.42:4242"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			Wrap(pass()).ServeHTTP(rec, req(t, http.MethodPost, "/api/chat/send", c.origin, "application/json"))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 — the guard must not break this caller", rec.Code)
			}
		})
	}
}

func TestGuardedResponseReflectsTheOriginAndNeverWildcards(t *testing.T) {
	rec := httptest.NewRecorder()
	Wrap(pass()).ServeHTTP(rec, req(t, http.MethodPost, "/api/chat/send", "http://localhost:4242", "application/json"))

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:4242" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the exact origin", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary = %q, want Origin — otherwise a shared cache can hand one origin's ACAO to another", got)
	}
}

func TestPreflight(t *testing.T) {
	t.Run("from a hostile origin is refused, not blanket-approved", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := req(t, http.MethodOptions, "/api/chat/send", "https://evil.example.com", "")
		r.Header.Set("Access-Control-Request-Method", "POST")
		Wrap(pass()).ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Access-Control-Allow-Origin = %q on a refused preflight, want none", got)
		}
	})

	t.Run("from the panel still succeeds", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := req(t, http.MethodOptions, "/api/chat/draft", "http://localhost:4242", "")
		r.Header.Set("Access-Control-Request-Method", "PUT")
		Wrap(pass()).ServeHTTP(rec, r)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		if !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "PUT") {
			t.Fatalf("Allow-Methods = %q, want it to include PUT", rec.Header().Get("Access-Control-Allow-Methods"))
		}
	})
}

// TestUploadIsExemptFromTheJSONGateButNotTheOriginCheck pins the one place
// the two gates deliberately come apart: /api/chat/upload is multipart by
// contract, so holding it to application/json would reject every legitimate
// upload — the origin check alone carries it.
func TestUploadIsExemptFromTheJSONGateButNotTheOriginCheck(t *testing.T) {
	rec := httptest.NewRecorder()
	Wrap(pass()).ServeHTTP(rec, req(t, http.MethodPost, "/api/chat/upload", "http://localhost:4242", "multipart/form-data; boundary=x"))
	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin multipart upload: status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	Wrap(pass()).ServeHTTP(rec, req(t, http.MethodPost, "/api/chat/upload", "https://evil.example.com", "multipart/form-data; boundary=x"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin multipart upload: status = %d, want 403", rec.Code)
	}
}

// TestUnguardedRoutesAreUntouched — the guard must be invisible to the read
// surface, including for a foreign origin (those routes were world-readable
// before and this ticket does not change that; see the package comment).
func TestUnguardedRoutesAreUntouched(t *testing.T) {
	for _, p := range []string{"/health", "/api/chat/history", "/api/chat/uploads/x.png"} {
		rec := httptest.NewRecorder()
		Wrap(pass()).ServeHTTP(rec, req(t, http.MethodGet, p, "https://evil.example.com", ""))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 — read routes must keep working", p, rec.Code)
		}
	}
}

// TestEventsIngressAndStreamAreBothGuarded pins the consequence of
// /api/chat/events becoming a POST ingress into the SSE hub: the path is
// guarded, and because the classifier is method-independent the GET stream on
// it is refused cross-origin too. That is a deliberate tightening relative to
// the TS server, where the same GET is accepted residue.
func TestEventsIngressAndStreamAreBothGuarded(t *testing.T) {
	for _, c := range []struct{ method, contentType string }{
		{http.MethodPost, "application/json"},
		{http.MethodGet, ""},
	} {
		rec := httptest.NewRecorder()
		Wrap(pass()).ServeHTTP(rec, req(t, c.method, "/api/chat/events", "https://evil.example.com", c.contentType))
		if rec.Code != http.StatusForbidden {
			t.Errorf("cross-origin %s /api/chat/events: status = %d, want 403", c.method, rec.Code)
		}
	}

	// Every real caller keeps working: the TS tailers and the CLI send no
	// Origin, the panel is same-origin.
	for _, c := range []struct{ name, origin string }{
		{"the TS tailers / CLI (no Origin)", ""},
		{"the panel itself", "http://localhost:4242"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			Wrap(pass()).ServeHTTP(rec, req(t, http.MethodPost, "/api/chat/events", c.origin, "application/json"))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 — the guard must not break this producer", rec.Code)
			}
		})
	}
}

// TestGuardingTheEventsPathGrantsNoCORSOnItsStream pins the other half of that
// classification. Guarding a path also turns the reflected ACAO on for every
// origin OriginAllowed accepts, which includes any page on the captain's LAN —
// so putting /api/chat/events in GuardedPaths for the sake of its POST ingress
// would otherwise hand a LAN page a readable SSE stream it could not read
// before. The refusal is the part that is stricter than the TS side; the grant
// must not come with it.
func TestGuardingTheEventsPathGrantsNoCORSOnItsStream(t *testing.T) {
	// The regression: an origin the guard ALLOWS still gets no CORS headers on
	// the stream, so a foreign page's EventSource runs but reads nothing.
	rec := httptest.NewRecorder()
	Wrap(pass()).ServeHTTP(rec, req(t, http.MethodGet, "/api/chat/events", "http://192.168.1.42:4242", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed LAN origin: status = %d, want 200", rec.Code)
	}
	for _, h := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers",
	} {
		if got := rec.Header().Get(h); got != "" {
			t.Errorf("GET /api/chat/events carries %s: %q, want it absent", h, got)
		}
	}
	// Vary is a cache directive, not a grant, and the response really does
	// differ by Origin (403 vs. the stream).
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}

	// The 403 the path is guarded for is untouched.
	rec = httptest.NewRecorder()
	Wrap(pass()).ServeHTTP(rec, req(t, http.MethodGet, "/api/chat/events", "https://evil.example.com", ""))
	if rec.Code != http.StatusForbidden {
		t.Errorf("hostile origin: status = %d, want 403", rec.Code)
	}

	// And every caller that actually streams today still gets its stream.
	for _, c := range []struct{ name, origin string }{
		{"a no-Origin client (curl, the CLI)", ""},
		{"the panel itself", "http://localhost:4242"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			Wrap(pass()).ServeHTTP(rec, req(t, http.MethodGet, "/api/chat/events", c.origin, ""))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
		})
	}

	// Method-scoped: the POST ingress is an ordinary guarded route and keeps
	// the reflected ACAO, so the carve-out cannot be read as "this path is
	// half-guarded".
	rec = httptest.NewRecorder()
	Wrap(pass()).ServeHTTP(rec, req(t, http.MethodPost, "/api/chat/events", "http://192.168.1.42:4242", "application/json"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST from an allowed LAN origin: status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://192.168.1.42:4242" {
		t.Errorf("POST ingress ACAO = %q, want the reflected origin", got)
	}
}

// The routes below have no Go handler yet — they are the parity ports still in
// flight from packages/server. Their guard entries were landed FIRST, on
// purpose: the one predictable way this boundary fails is a route added by
// someone editing internal/handlers who never opens guard.go, and an entry that
// precedes its handler removes that window entirely.
//
// So this test is not a restatement of the map. It is the thing that makes
// landing-early worth doing: it fails if a port arrives and someone deletes the
// entry to make a caller work, which is precisely the moment the route becomes
// reachable and the refusal starts to matter.
func TestInFlightPortsAreGuardedBeforeTheirHandlersExist(t *testing.T) {
	for _, path := range []string{
		"/api/chat/clear",
		"/api/chat/system",
		"/api/chat/navigate",
		"/api/chat/reload",
		"/api/chat/device-cmd",
		"/api/chat/declare-channel",
		"/api/chat/eval",
		"/api/chat/eval-push",
		"/api/chat/plugin/cursorless/rpc",
		"/api/chat/tts",
		"/api/chat/tts-report",
		"/api/chat/tts-correction",
		"/api/chat/tts/validate-splits",
		"/api/chat/tts-event",
	} {
		if !IsGuarded(path) {
			t.Errorf("%s is outside the guard — a cross-origin page could reach it the moment its handler lands", path)
		}
	}
}

// Both exempt ports keep the origin check; only the content-type layer drops.
// A test that asserted the exemption alone would pass just as happily if the
// route had been dropped from GuardedPaths altogether, which is the mistake
// worth catching.
func TestTheTwoExemptPortsAreStillInsideTheGuard(t *testing.T) {
	for _, path := range []string{
		"/api/chat/plugin/cursorless/rpc",
		"/api/chat/tts/validate-splits",
	} {
		if !IsGuarded(path) {
			t.Errorf("%s: the JSON exemption drops one layer, not the boundary", path)
		}
		if !jsonExemptPaths[path] {
			t.Errorf("%s: expected the content-type gate to be exempted for this caller", path)
		}
	}
}

// The plugin subtree is guarded as a WHOLE, not route by route.
//
// This is the test that distinguishes a prefix from a longer list. The exact
// map already held /api/chat/plugin/cursorless/rpc, so a test that only
// checked the routes which exist today would pass against exact matching too
// and prove nothing about the property being claimed. The route that mattered
// was the sibling nobody added: /response, a POST that mutates rpcWaiters and
// resolves a pending RPC with the caller's own `result`. It reached its
// handler from any origin, under any content type, with no preflight.
//
// So the deliberately-nonexistent path below is the assertion, not filler: the
// claim is that a plugin route added LATER is guarded before anyone thinks
// about it, and only a path that no handler serves can test that claim.
func TestEveryPluginRouteIsGuardedIncludingOnesNotWrittenYet(t *testing.T) {
	for _, path := range []string{
		"/api/chat/plugin/cursorless/rpc",      // exists, was already in the map
		"/api/chat/plugin/cursorless/response", // exists, was NOT — the live gap
		"/api/chat/plugin/not-invented-yet/do", // the property, stated as a test
	} {
		if !IsGuarded(path) {
			t.Errorf("%s: the whole /api/chat/plugin/ subtree is inside the guard", path)
		}
	}
}

// A cross-origin POST to the formerly-open plugin route is refused end to end.
//
// IsGuarded is the classification; this is the consequence. Asserting only the
// former would leave the boundary's actual behaviour unpinned — the same shape
// as a check that passes because it never looked.
func TestWrapRefusesCrossOriginOnAPluginRouteNotInTheExactMap(t *testing.T) {
	rec := httptest.NewRecorder()
	Wrap(pass()).ServeHTTP(rec, req(t, http.MethodPost,
		"/api/chat/plugin/cursorless/response", "https://evil.example.com", "application/json"))

	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST to the plugin response route: status = %d, want 403", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "reached the handler") {
		t.Errorf("the request reached the handler: %s", body)
	}
}

// The panel's own POST to that route still works.
//
// A guard is only as good as its not breaking the caller it exists to protect;
// packages/client/src-plugins/cursorless.ts posts /response same-origin with
// an explicit Content-Type: application/json, so both layers cost it nothing.
// Pinned because "tighten the boundary" and "break voice editing on the
// captain's box" are one careless commit apart.
func TestThePanelsOwnPluginResponsePostStillPasses(t *testing.T) {
	rec := httptest.NewRecorder()
	Wrap(pass()).ServeHTTP(rec, req(t, http.MethodPost,
		"/api/chat/plugin/cursorless/response", "http://localhost:4242", "application/json"))

	if rec.Code != http.StatusOK {
		t.Errorf("same-origin panel POST: status = %d, want 200", rec.Code)
	}
}

// The trailing slash on /api/chat/agents/ is load-bearing.
//
// GET /api/chat/agents is a read route and stays open — Go sends no ACAO on
// unguarded routes at all, so a foreign page cannot read the response. (TS
// guards its own /api/chat/agents because unguarded routes THERE still carry
// the legacy wildcard CORS. Same boundary, opposite defaults, both correct.)
// A prefix written without the slash would swallow the exact path and quietly
// reverse a decision each server made on its own evidence, so this pins the
// two apart rather than trusting the string literal to keep its last
// character.
func TestTheAgentsPrefixDoesNotSwallowTheAgentsReadRoute(t *testing.T) {
	if IsGuarded("/api/chat/agents") {
		t.Error("/api/chat/agents is a read route on this server and must stay unguarded")
	}
	if !IsGuarded("/api/chat/agents/some-agent-id") {
		t.Error("DELETE /api/chat/agents/:id mutates the registry and must be guarded")
	}
}

// /api/debug/ is guarded before its handler exists, same discipline as the
// in-flight exact entries above: its GET response is keyed BY DEVICE ID, which
// is the identifier the whole guard exists to keep out of a foreign origin's
// reach, and its POST writes into the timing buffer. Landing the prefix first
// means the port cannot arrive open.
func TestTheDebugSubtreeIsGuardedBeforeItsHandlerExists(t *testing.T) {
	if !IsGuarded("/api/debug/input-timing") {
		t.Error("/api/debug/ is keyed by device id and must be guarded before the port lands")
	}
}
