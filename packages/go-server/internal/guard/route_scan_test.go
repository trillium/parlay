package guard

// This file is the PARSE half of the route-coverage gate: it reads the routes
// the server binary puts on an http.ServeMux out of the source that registers
// them, so TestEveryRegisteredRouteIsGuardedOrExplained can demand a decision
// for each one.
//
// WHAT THE PARSE FOLLOWS, and why it cannot silently stop following it:
//
// It follows the first-party import closure of every `package main` in this
// module — starting from cmd/parlay-server, which is where the mux is built,
// down through every package that binary imports. It is deliberately NOT a
// directory list. An earlier version of this gate walked internal/handlers and
// nothing else, which is a guess about where registrations happen to live: the
// same mux was also being registered from cmd/parlay-server/main.go (/health,
// /fleet/ and the SPA catch-all /), so an unguarded route added there passed
// the gate silently. The import closure is the fact — a package the binary does
// not import cannot register a route on its mux, and a package it does import
// is walked whether or not anybody remembered to name its directory here.
//
// Four properties keep that from quietly degrading:
//  1. Module-wide parse. Every non-test .go file in the module is parsed, and a
//     parse error is fatal. There is no directory the scan skips, so a new
//     package cannot be invisible to it.
//  2. No `package main` is fatal, not an empty route set: with no seed there
//     is no closure and nothing is examined.
//  3. A registration the scan cannot FOLLOW is a reported problem, never a
//     skipped line: a `.Handle`/`.HandleFunc` call on any receiver other than
//     the recognised mux, a call with no receiver at all, a call with no path
//     argument, and a path argument that is not a string literal or a
//     package-level string constant all produce a problem naming the file and
//     line. So renaming the mux variable, routing through a subrouter, or
//     moving registration behind a helper this file cannot read fails the build
//     instead of emptying the route set.
//  4. Every package in the module that registers routes must be inside the
//     seed closure. A registration-carrying package that no `package main`
//     imports is reported: either it is dead code that should not be there, or
//     the seed discovery is looking in the wrong place. Both deserve a human.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// muxReceiver is the receiver name this scan recognises for
// (*http.ServeMux).Handle and .HandleFunc. Anything else named Handle or
// HandleFunc is reported rather than ignored — see property 3 above.
const muxReceiver = "mux"

// routeRegistration is one path argument passed to mux.Handle/HandleFunc,
// with the source position that registered it so a failure names a line.
type routeRegistration struct {
	path string
	pos  string
}

// scannedPkg is one package of this module after parsing.
type scannedPkg struct {
	importPath string
	dir        string
	name       string
	files      []*ast.File
	imports    []string
	// registrations are the resolved route paths this package puts on a mux.
	registrations []string
	// registers is true when the package contains at least one Handle-shaped
	// call, used by the closure check (property 4).
	registers bool
	// problems are per-package complaints the scan cannot resolve on its own.
	problems []string
}

// scanResult is what the gate needs: the routes, and everything the scan could
// not decide by itself. Problems are returned rather than raised as test
// failures so a test can assert ON them (TestTheGateSees...) instead of a human
// having to break the real tree to watch the build go red.
type scanResult struct {
	// routes is the sorted, de-duplicated set of paths put on a mux anywhere
	// in the seed closure.
	routes []string
	// problems are registration shapes the scan refused to ignore.
	problems []string
	// closure is the number of packages reached from the `package main` seeds.
	closure int
	// roots names the seed packages, for a failure message that says what the
	// scan started from.
	roots []string
}

// scanRoutes parses every non-test package under root (the directory holding
// go.mod) and returns the routes registered anywhere in the first-party import
// closure of every `package main` in the module.
//
// The returned error is reserved for the two states in which there is nothing
// to examine at all: no go.mod, or no package main to seed from. Everything
// else is a problem.
func scanRoutes(root string) (*scanResult, error) {
	modPath, err := modulePath(root)
	if err != nil {
		return nil, err
	}
	pkgs, err := parseModule(root, modPath)
	if err != nil {
		return nil, err
	}

	res := &scanResult{}
	closure := map[string]bool{}
	queue := []string{}
	for _, p := range pkgs {
		if p.name == "main" {
			queue = append(queue, p.importPath)
			res.roots = append(res.roots, p.importPath)
		}
	}
	sort.Strings(res.roots)
	if len(queue) == 0 {
		return nil, fmt.Errorf("no package main anywhere in %s — the route scan has no seed to follow, "+
			"so it is examining nothing; point scanRoutes at the module that builds the server", root)
	}
	for i := 0; i < len(queue); i++ {
		cur := queue[i]
		if closure[cur] {
			continue
		}
		closure[cur] = true
		res.closure++
		for _, imp := range pkgs[cur].imports {
			// First-party only: an import outside this module cannot put a
			// route on this binary's mux, and third-party sources are not in
			// the tree to parse.
			if _, ok := pkgs[imp]; ok && !closure[imp] {
				queue = append(queue, imp)
			}
		}
	}

	for _, p := range pkgs {
		if closure[p.importPath] {
			res.routes = append(res.routes, p.registrations...)
			res.problems = append(res.problems, p.problems...)
			continue
		}
		// Property 4: a package that registers routes but that no package main
		// in this module imports.
		if p.registers {
			res.problems = append(res.problems, fmt.Sprintf(
				"%s puts routes on a mux but no package main in this module imports it — "+
					"either that is dead code, or this scan is no longer following the registration; "+
					"resolve it here rather than shipping a route the gate cannot see",
				p.importPath))
		}
	}
	res.routes = dedupe(res.routes)
	sort.Strings(res.problems)
	return res, nil
}

// modulePath reads the module path out of <root>/go.mod.
func modulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod at %s: %w", root, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			if p := strings.TrimSpace(rest); p != "" {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("no module directive in %s/go.mod", root)
}

// parseModule parses every non-test .go file under root, keyed by import path.
// A parse error is fatal here rather than a reported problem: a file the module
// cannot parse is a build failure anyway, and a scan that skipped it would
// report a clean result it has no basis for.
func parseModule(root, modPath string) (map[string]*scannedPkg, error) {
	goFiles := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && skipDir(name) {
				return fs.SkipDir
			}
			return nil
		}
		// Test files are excluded throughout: they build their own muxes for
		// their own assertions, and they never ship.
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		goFiles[filepath.Dir(path)] = append(goFiles[filepath.Dir(path)], path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}

	pkgs := map[string]*scannedPkg{}
	for dir, files := range goFiles {
		sort.Strings(files)
		p := &scannedPkg{importPath: importPathFor(root, modPath, dir), dir: dir}
		fset := token.NewFileSet()
		for _, file := range files {
			f, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", file, err)
			}
			if p.name == "" {
				p.name = f.Name.Name
			} else if f.Name.Name != p.name {
				return nil, fmt.Errorf("%s mixes package %s and %s in one directory", dir, p.name, f.Name.Name)
			}
			p.files = append(p.files, f)
		}
		p.collect(fset, root)
		pkgs[p.importPath] = p
	}
	return pkgs, nil
}

// skipDir names directories that hold no Go package of this module's own.
func skipDir(name string) bool {
	switch name {
	case "testdata", "node_modules", "vendor":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// importPathFor maps a directory to its module-relative import path.
func importPathFor(root, modPath, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return modPath
	}
	return modPath + "/" + filepath.ToSlash(rel)
}
