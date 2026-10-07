package guard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	"/api/chat/input-events": "read: the input-seam ledger; message ids, channel names and per-channel poll timestamps are already unguarded at /api/chat/history",
	"/api/chat/commands":     "read: the live-command snapshot. Same class as /api/chat/agents; its three POST siblings ARE guarded",
	"/api/chat/version":      "read: bundle version string, polled by every SSE client",
	"/api/chat/pages":        "read: the page manifest the panel's page picker renders",
	"/api/chat/plugins":      "read: static plugin manifests. The mutating plugin subtree is guarded by the /api/chat/plugin/ prefix",
	// The mux registers the UPLOAD SERVING SUBTREE under its prefix
	// constant; /api/chat/uploads/<name> is the URL shape it answers.
	"/api/chat/uploads/": "read: serves an uploaded image by server-generated name, same class as /api/chat/agents",
}

// TestEveryRegisteredRouteIsGuardedOrExplained is the gate.
//
// It parses every non-test .go file in internal/handlers for the paths put on
// the mux, then requires each one to be either guarded (exactly, or under a
// guarded prefix) or to appear in TestUnguardedRoutes with a reason. Adding a
// route therefore fails the build with a message that says which of the two
// halves it failed, rather than shipping and being discovered later.
func TestEveryRegisteredRouteIsGuardedOrExplained(t *testing.T) {
	routes := registeredRoutes(t)

	if len(routes) == 0 {
		// The vacuous-pass guard, and the reason this test parses source
		// instead of calling Register: a refactor that renames mux.HandleFunc,
		// moves registration behind a helper this parser cannot follow, or
		// moves the package would otherwise turn the gate silently green.
		t.Fatal("parsed zero routes from internal/handlers — the gate is no longer looking at anything; " +
			"update registeredRoutes to follow the new registration shape")
	}

	for _, r := range routes {
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
		if !contains(routes, path) {
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

// registeredRoutes returns every path internal/handlers puts on an
// http.ServeMux, by parsing the registrations rather than calling Register.
//
// Calling Register would need a live Store and Hub and would only see routes
// that package wires itself, missing RegisterData / RegisterPlugins (which
// cmd/parlay-server calls separately) — so this reads the source. It
// understands the two shapes registration takes here: a string literal, and a
// package-level string constant (uploadURLPrefix). A constant this parser
// cannot resolve fails the test loudly instead of silently dropping the route.
func registeredRoutes(t *testing.T) []string {
	t.Helper()

	dir := filepath.Join("..", "handlers")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}

	consts := map[string]string{}
	var out []string
	add := func(expr ast.Expr) {
		switch e := expr.(type) {
		case *ast.BasicLit:
			if e.Kind != token.STRING {
				t.Errorf("route registered from a non-string literal: %s", e.Value)
				return
			}
			v, err := strconv.Unquote(e.Value)
			if err != nil {
				t.Errorf("unquote route %s: %v", e.Value, err)
				return
			}
			out = append(out, v)
		case *ast.Ident:
			v, ok := consts[e.Name]
			if !ok {
				t.Errorf("route registered from identifier %q, which this parser cannot resolve — "+
					"a route just went unexamined; update registeredRoutes", e.Name)
				return
			}
			out = append(out, v)
		default:
			t.Errorf("route registered from an expression this parser cannot resolve (%T) — "+
				"a route just went unexamined; update registeredRoutes", expr)
		}
	}

	for _, pkg := range pkgs {
		// Two passes over each file's AST: consts first, then registrations.
		// ast.Inspect visits in source order and ParseDir hands back files in
		// map order, so a single pass would resolve uploadURLPrefix only when
		// upload.go happened to be visited before data.go — a gate that fails
		// at random is worse than no gate.
		for _, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				d, ok := n.(*ast.GenDecl)
				if !ok || d.Tok != token.CONST {
					return true
				}
				for _, s := range d.Specs {
					vs, ok := s.(*ast.ValueSpec)
					if !ok || len(vs.Values) != 1 || len(vs.Names) != 1 {
						continue
					}
					if lit, ok := vs.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil {
							consts[vs.Names[0].Name] = v
						}
					}
				}
				return true
			})
		}
		for _, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				d, ok := n.(*ast.CallExpr)
				if !ok || len(d.Args) == 0 {
					return true
				}
				sel, ok := d.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				recv, ok := sel.X.(*ast.Ident)
				if !ok || recv.Name != "mux" {
					return true
				}
				if sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle" {
					return true
				}
				add(d.Args[0])
				return true
			})
		}
	}

	sort.Strings(out)
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
