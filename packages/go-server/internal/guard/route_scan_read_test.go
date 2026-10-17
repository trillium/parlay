package guard

// The READ half of the route scan: per-package constant resolution and the
// reading of each Handle-shaped call. See route_scan_test.go for what the
// scan follows and the four properties that keep it from going vacuous.

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// collect resolves one package's string constants and then its route
// registrations. Two passes, in that order: ast.Inspect visits in source order
// and a single pass would resolve a constant only when the file declaring it
// happened to be visited first — a gate that fails at random is worse than no
// gate.
func (p *scannedPkg) collect(fset *token.FileSet, root string) {
	imports := map[string]bool{}
	for _, f := range p.files {
		for _, imp := range f.Imports {
			if path, err := strconv.Unquote(imp.Path.Value); err == nil {
				imports[path] = true
			}
		}
	}

	// Pass one: package-level string constants, resolved across every file of
	// the package.
	consts := map[string]string{}
	for _, f := range p.files {
		for _, s := range f.Decls {
			d, ok := s.(*ast.GenDecl)
			if !ok || d.Tok != token.CONST {
				continue
			}
			for _, spec := range d.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if v, err := strconv.Unquote(lit.Value); err == nil {
					consts[vs.Names[0].Name] = v
				}
			}
		}
	}

	// Pass two: the registrations, which may name a constant pass one resolved.
	for _, f := range p.files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isRegistrationCall(call) {
				return true
			}
			p.readRegistration(fset, root, call, consts)
			return true
		})
	}

	for imp := range imports {
		p.imports = append(p.imports, imp)
	}
	sort.Strings(p.imports)
}

// isRegistrationCall reports whether call looks like a route registration
// rather than an arbitrary method that happens to be called Handle.
func isRegistrationCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		if id, ok := call.Fun.(*ast.Ident); ok {
			return id.Name == "Handle" || id.Name == "HandleFunc"
		}
		return false
	}
	return sel.Sel.Name == "Handle" || sel.Sel.Name == "HandleFunc"
}

// readRegistration turns one Handle-shaped call into a route, or into a
// problem. Every shape it cannot follow becomes a problem naming the line —
// that is what keeps the scan from silently covering less than it used to.
func (p *scannedPkg) readRegistration(fset *token.FileSet, root string, call *ast.CallExpr, consts map[string]string) {
	p.registers = true
	pos := rel(fset.Position(call.Pos()).String(), root)

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		p.problems = append(p.problems, pos+
			": a Handle/HandleFunc call with no receiver — the route scan cannot tell what it registers on; "+
			"update readRegistration in route_scan_test.go")
		return
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok || recv.Name != muxReceiver {
		p.problems = append(p.problems, pos+
			": a "+sel.Sel.Name+" call this scan cannot follow (receiver is not "+muxReceiver+") — "+
			"a route just went unexamined; update readRegistration in route_scan_test.go")
		return
	}
	if len(call.Args) == 0 {
		p.problems = append(p.problems, pos+
			": "+sel.Sel.Name+" with no path argument — the route scan cannot tell what it registered")
		return
	}
	switch e := call.Args[0].(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			p.problems = append(p.problems, pos+": route registered from a non-string literal: "+e.Value)
			return
		}
		v, err := strconv.Unquote(e.Value)
		if err != nil {
			p.problems = append(p.problems, pos+": unquote route "+e.Value+": "+err.Error())
			return
		}
		p.registrations = append(p.registrations, v)
	case *ast.Ident:
		v, ok := consts[e.Name]
		if !ok {
			p.problems = append(p.problems, pos+
				": route registered from identifier "+strconv.Quote(e.Name)+", which this scan cannot resolve — "+
				"a route just went unexamined; update readRegistration in route_scan_test.go")
			return
		}
		p.registrations = append(p.registrations, v)
	default:
		p.problems = append(p.problems, pos+
			": route registered from an expression this scan cannot resolve — a route just went "+
			"unexamined; update readRegistration in route_scan_test.go")
	}
}

// rel trims the module root off a position so a message names
// cmd/parlay-server/main.go:97 rather than a temporary directory.
func rel(pos, root string) string {
	return strings.TrimPrefix(pos, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
