package guard

// Proof that the widened scan actually closed the hole, and proof that it
// cannot go vacuous. Each test here builds a throwaway module and runs the same
// scan the gate runs, so the before-and-after is executable rather than a claim
// in a comment: the OLD parse (one directory, internal/handlers) is
// reproduced alongside the new one on the identical source.

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeModule writes a minimal module with the given files, keyed by
// slash-separated module-relative path, and returns its root.
func fakeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module parlay/go-server\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, body := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// scanOneDir is the OLD parse, reproduced exactly: one directory, non-test
// files only, no notion of a seed or a closure. It exists here so a test can
// show what the gate could and could not see, not to serve the gate.
func scanOneDir(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	var routes []string
	for _, pkg := range pkgs {
		p := &scannedPkg{dir: dir}
		for _, f := range pkg.Files {
			p.files = append(p.files, f)
		}
		p.collect(fset, dir)
		routes = append(routes, p.registrations...)
	}
	return routes
}

const (
	fakeMainWithForgedRoute = `package main

import (
	"net/http"

	"parlay/go-server/internal/handlers"
)

func main() {
	mux := http.NewServeMux()
	handlers.Register(mux)
	// A new route registered on the main file, in the pattern this gate used
	// to be blind to.
	mux.HandleFunc("/api/chat/forged-register", forged)
	mux.Handle("/", http.NotFoundHandler())
}

func forged(http.ResponseWriter, *http.Request) {}
`

	fakeHandlers = `package handlers

import "net/http"

// Register is the shape the real package has: routes go on the mux the binary
// passes in.
func Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/chat/send", send)
}

func send(http.ResponseWriter, *http.Request) {}
`
)

// TestTheGateSeesARouteRegisteredInMainGo is the before-and-after.
//
// The source below registers /api/chat/forged-register from cmd/parlay-server
// and one route from internal/handlers. Before this change the gate parsed
// internal/handlers only and saw exactly one route — the forged one was
// invisible and an unguarded route there passed silently. After, both are
// scanned, and the forged one is reported as neither guarded nor explained,
// which is the failure the build now raises.
func TestTheGateSeesARouteRegisteredInMainGo(t *testing.T) {
	root := fakeModule(t, map[string]string{
		"cmd/parlay-server/main.go": fakeMainWithForgedRoute,
		"internal/handlers/reg.go":  fakeHandlers,
	})

	res, err := scanRoutes(root)
	if err != nil {
		t.Fatalf("scanRoutes: %v", err)
	}
	for _, p := range res.problems {
		t.Errorf("unexpected scan problem: %s", p)
	}

	// BEFORE: the old one-directory parse, pointed at the same source.
	before := scanOneDir(t, filepath.Join(root, "internal", "handlers"))
	if contains(before, forgedRoute) {
		t.Fatal("the old parse is not reproduced by this test — it already saw cmd/parlay-server, " +
			"so the before/after comparison here is meaningless")
	}
	if !contains(before, "/api/chat/send") {
		t.Fatalf("the old parse should have seen the handlers route, got %v", before)
	}

	// AFTER: the widened scan sees both, and the gate's verdict on the route
	// the old parse could not see is the failure it exists to produce.
	if !contains(res.routes, forgedRoute) {
		t.Fatalf("widened scan missed %s; it saw %v", forgedRoute, res.routes)
	}
	if IsGuarded(forgedRoute) {
		t.Fatalf("%s is guarded in this repo's real policy; the point of this test is the unguarded case", forgedRoute)
	}
	if _, explained := TestUnguardedRoutes[forgedRoute]; explained {
		t.Fatalf("%s must not appear in TestUnguardedRoutes", forgedRoute)
	}
	if !gateRejects(forgedRoute) {
		t.Errorf("the gate accepts %s, which is registered from cmd/parlay-server and unguarded — "+
			"the hole this change closed is open again", forgedRoute)
	}
}

const forgedRoute = "/api/chat/forged-register"

// TestTheRealServerBinaryRoutesAreAllClassified pins the three routes
// cmd/parlay-server puts on the mux — the ones this gate could not see before.
// Each is either guarded or explained with a reason, and the scan finds them in
// the main file itself, not just under internal/handlers.
func TestTheRealServerBinaryRoutesAreAllClassified(t *testing.T) {
	res := registeredRoutes(t)

	for _, p := range []string{"/health", "/fleet/", "/"} {
		if !contains(res.routes, p) {
			t.Errorf("%s is registered on the server binary's mux but the scan did not find it", p)
			continue
		}
		reason, explained := TestUnguardedRoutes[p]
		switch {
		case IsGuarded(p) && explained:
			t.Errorf("%s is both guarded and explained", p)
		case IsGuarded(p):
			t.Errorf("%s is guarded; drop its TestUnguardedRoutes entry", p)
		case !explained:
			t.Errorf("%s is registered on the mux and unguarded with no explanation in TestUnguardedRoutes", p)
		case len(reason) < 40:
			t.Errorf("%s is explained with %q — a reason must name something a reviewer can check, "+
				"not just assert 'read-only'", p, reason)
		}
	}

	// And the closure is not the one package: the widened scan must still see
	// internal/handlers too, or the classification above would be all it does.
	if !contains(res.routes, "/api/chat/send") {
		t.Error("the widened scan lost internal/handlers; the closure walk is not reaching the binary's packages")
	}
	if res.closure < 2 {
		t.Errorf("the scan followed %d packages from seeds %v; the server binary imports several, "+
			"so this is narrower than it should be", res.closure, res.roots)
	}
}

// gateRejects reports whether the gate's rule — guarded, or explained with a
// reason — fails for path. It is the decision TestEveryRegisteredRouteIsGuardedOrExplained
// makes, factored out so a test can assert on it.
func gateRejects(path string) bool {
	_, explained := TestUnguardedRoutes[path]
	return !IsGuarded(path) && !explained
}

func hasProblem(problems []string, want string) bool {
	for _, p := range problems {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}
