package guard

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file is the ENFORCEMENT half of the rule the GuardedPaths doc comment
// states — "a new mutating route is UNGUARDED until it is added here". Before
// it existed, the live-command registry's three report routes shipped outside
// the boundary carrying a hand-rolled copy of the content-type gate, on a
// comment asserting this server had no guard at all. Nothing failed: the
// hand-rolled gate genuinely does block a CORS simple request, so every
// existing test stayed green while a mutating route sat outside the only
// boundary the server has.
//
// A hand-maintained list cannot catch this, because the list is the thing that
// was wrong. So the routes are read out of the source that registers them.

// TestUnguardedRoutes is the complete, reviewed list of routes that are
// deliberately OUTSIDE the guard, each with the reason it is not a defect.
//
// A new member must come with a reason, and the reason must name something
// checkable — a caller, a convention, a precedent — because "read-only" alone
// is the sentence that hides a mutating route. There is no wildcard entry and
// no prefix entry: a route either earns its place by name or it is guarded.
var TestUnguardedRoutes = map[string]string{
	// Reads. Guarding these would newly reflect an Access-Control-Allow-Origin
	// to every LAN/loopback origin the guard allows, on bodies that have never
	// sent CORS headers — a widening, not a narrowing. Unguarded here they send
	// no ACAO at all, so a foreign page's read executes and its body stays
	// unreadable. See divergence 1 in the package comment.
	"/api/chat/history": "read: chat history, no identifiers and no device uuid",
	"/api/chat/agents":  "read: registered agent ids. Deliberately asymmetric with the /api/chat/agents/ prefix below — see that entry",
	// The input-seam ledger hands out message ids and channel names — the same
	// pair /api/chat/history already returns unguarded — plus short typed
	// reason tokens and, since the listener half was added, per-channel poll
	// counts and timestamps. No device uuid, no message text (the ledger never
	// stores any), no caller identity (a poll timestamp names a channel, not
	// who asked), and nothing writable: an unknown ?inputId= is an empty list.
	"/api/chat/input-events":      "read: the input-seam ledger; message ids, channel names and per-channel poll timestamps are already unguarded at /api/chat/history",
	"/api/chat/commands":          "read: the live-command snapshot. Same class as /api/chat/agents; its three POST siblings ARE guarded",
	"/api/chat/client":            "read: the onboarding document (listen address, admit list, client contract). Sends no CORS headers, so a foreign page cannot read it; the admit list is a CORS allowance, not access control, on a server that has no authentication",
	"/api/chat/version":           "read: bundle version string, polled by every SSE client",
	"/demo":                       "read: static HTML for the talk demo; the recorded fixture is inlined and holds no real data. Every /api/chat/demo/* route it calls is guarded",
	"/slide":                      "read: static HTML for the phone slide button; embeds no secret. On a secure origin it registers /slide/sw.js, which precaches that static shell and does not call /api. The POST it drives, /api/chat/slide, is guarded and bearer-token gated",
	"/slide/manifest.webmanifest": "read: static web app manifest for the phone slide page; no secret and no token",
	"/slide/sw.js":                "read: static service worker for the slide page shell; precaches that shell only and does not cache /api",
	"/slide/icon-180.png":         "read: static PNG apple-touch icon for the slide page; no secret",
	"/slide/icon-192.png":         "read: static PNG icon for the slide page; no secret",
	"/slide/icon-512.png":         "read: static PNG icon for the slide page; no secret",
	"/api/chat/pages":             "read: the page manifest the panel's page picker renders",
	"/api/chat/plugins":           "read: static plugin manifests. The mutating plugin subtree is guarded by the /api/chat/plugin/ prefix",
	// The mux registers the UPLOAD SERVING SUBTREE under its prefix
	// constant; /api/chat/uploads/<name> is the URL shape it answers.
	"/api/chat/uploads/": "read: serves an uploaded image by server-generated name, same class as /api/chat/agents",

	// The three routes cmd/parlay-server/main.go puts on the same mux. They
	// were invisible to this gate until the parse followed the registration
	// call chain out of internal/handlers — see registeredRoutes below.
	"/health": "read: GET-only liveness (every other method is 405). The body is {ok, messages, agents} where agents is a COUNT, not an id — so it neither mutates nor hands out an identifier a cross-origin page could aim a mutating route with. Checkable: registerHealth in cmd/parlay-server/main.go",
	"/fleet/": "read: static file serving of the built webview bundle under <assets-dir>/fleet via internal/static.Handler (http.ServeFile, with a path-traversal check). It writes nothing and holds no server state. Registered at cmd/parlay-server/main.go",
	"/":       "read: the SPA catch-all serving packages/client/dist through the same internal/static.Handler, falling back to index.html. It is mounted LAST so /api/* is never shadowed, and it neither mutates nor discloses an identifier",
}

// TestEveryRegisteredRouteIsGuardedOrExplained is the gate.
//
// It scans every package the server binary imports — starting from its
// `package main`, following the registration call chain rather than a
// directory list — for the paths put on a mux, then requires each one to be
// either guarded (exactly, or under a guarded prefix) or to appear in
// TestUnguardedRoutes with a reason. Adding a route therefore fails the build
// with a message that says which of the two halves it failed, rather than
// shipping and being discovered later.
//
// A route registered on cmd/parlay-server's own mux counts like any other: the
// gate does not care which file did the registering.
func TestEveryRegisteredRouteIsGuardedOrExplained(t *testing.T) {
	res := registeredRoutes(t)

	for _, p := range res.problems {
		t.Error(p)
	}

	if len(res.routes) == 0 {
		// The vacuous-pass guard, and the reason this test parses source
		// instead of calling Register: a refactor that renames mux.HandleFunc,
		// moves registration behind a helper this scan cannot follow, or moves
		// the package would otherwise turn the gate silently green. It covers
		// the widened set now, not just internal/handlers — the seed closure
		// starts at the server binary and the problems above have already
		// reported any registration it could not read.
		t.Fatal("parsed zero routes from the server binary's package closure " +
			"(seeds: " + joinPaths(res.roots) + ") — the gate is no longer looking at anything; " +
			"update route_scan_test.go to follow the new registration shape")
	}

	for _, r := range res.routes {
		guarded := IsGuarded(r)
		_, explained := TestUnguardedRoutes[r]
		switch {
		case guarded && explained:
			t.Errorf("%s is guarded AND listed in TestUnguardedRoutes — pick one; a stale entry hides the next regression", r)
		case !guarded && !explained:
			t.Errorf("%s is registered on the mux but is neither guarded nor in TestUnguardedRoutes.\n"+
				"  If it mutates state or hands out an identifier, add it to GuardedPaths (with a comment saying why).\n"+
				"  If it is genuinely a read, add it to TestUnguardedRoutes with a reason a reviewer can check.", r)
		}
	}

	// The reverse direction: an explanation for a route nobody registers is a
	// stale entry, and stale entries are how an allowlist quietly stops
	// describing reality.
	for path := range TestUnguardedRoutes {
		if !contains(res.routes, path) {
			t.Errorf("TestUnguardedRoutes explains %s but no handler registers it — stale entry, remove it", path)
		}
	}
}

// TestTheLiveCommandReportRoutesAreInsideTheGuard pins this iteration's fix
// specifically. The coverage test above would also pass if these three were
// explained away as reads, so this says plainly what changed and why: they
// write registry rows, and a forged cross-origin POST with a JSON content type
// used to be accepted.
func TestTheLiveCommandReportRoutesAreInsideTheGuard(t *testing.T) {
	for _, p := range []string{"/api/chat/command-start", "/api/chat/command-heartbeat", "/api/chat/command-end"} {
		if !IsGuarded(p) {
			t.Errorf("%s writes the live-command registry and must be guarded", p)
		}
		// And the gate the guard adds on top of the handler's own: a foreign
		// origin is refused before the content-type check is even consulted.
		rec := httptest.NewRecorder()
		Wrap(pass()).ServeHTTP(rec, req(t, http.MethodPost, p, "https://evil.example.com", "application/json"))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s from a foreign origin with a JSON content type: status = %d, want 403", p, rec.Code)
		}
	}
	// The read half stays open — the /api/chat/agents precedent, and a guard
	// there would newly hand every allowed LAN origin an ACAO on a body that
	// has never sent one.
	if IsGuarded("/api/chat/commands") {
		t.Error("/api/chat/commands is a read route and must stay unguarded")
	}
}

// registeredRoutes scans the whole server binary for the paths it puts on a
// mux, by parsing the source rather than calling Register.
//
// Calling Register would need a live Store and Hub, would only see routes the
// handlers package wires itself, and would miss the three routes
// cmd/parlay-server registers on the mux it builds. scanRoutes (see
// route_scan_test.go) therefore parses the first-party import closure of every
// package main in this module. This wrapper only turns a scan error into a
// test failure and hands the result to the gate.
func registeredRoutes(t *testing.T) *scanResult {
	t.Helper()

	root := moduleRoot(t, ".")
	res, err := scanRoutes(root)
	if err != nil {
		t.Fatalf("scan routes: %v", err)
	}
	return res
}

// moduleRoot walks up from start to the directory holding the go.mod this
// package belongs to. Read from disk rather than counting "../"s out, so a
// moved package does not silently make the gate scan some other module.
func moduleRoot(t *testing.T, start string) string {
	t.Helper()
	dir, err := filepath.Abs(start)
	if err != nil {
		t.Fatalf("abs %s: %v", start, err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod at or above %s — the route scan has no module to read", start)
		}
		dir = parent
	}
}

func joinPaths(in []string) string {
	if len(in) == 0 {
		return "(none)"
	}
	return strings.Join(in, ", ")
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
