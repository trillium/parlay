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
