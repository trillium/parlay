package main

// The `handoff` store is a beads-store wrapper from the author's federation
// (the same family as task/inbox/robots), NOT a command this repo ships — so
// on a plain clone `handoff create` and `handoff show` do not exist. Iterations
// 14/16/17 of the onboarding work found the same defect in six places: an
// identity --submit refusal, context-check's ROTATE line, doctor's pointer
// note, drawdown's closing recipe, the respawn recovery prompt, and BOTH
// halves of `parlay claim`'s printed brief. Every one of them is the same
// shape: a runtime string that tells its reader to run a command their machine
// may not have.
//
// Nothing enforced that. `parlay help --help` has its own caveat gate
// (internal/help/help_test.go) because help text is static and cannot probe
// PATH, but RUNTIME output can — and the runtime population had no gate at
// all, so each fix was a one-off someone had to remember to make.
//
// This gate closes the loop: every non-test Go file under internal/ that puts
// a `handoff` subcommand in a string literal must either branch on
// resolvehandoff.StoreAvailable, or be on the allowlist below with a stated
// reason for why a static mention is correct there. A new prescription is
// therefore a build failure, not a support ticket.
//
// The gate works at DECLARATION granularity: once any function in a file is
// store-aware, a new `handoff create` in a different function of the same file
// must still fail the build.
import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// handoffSubcommands are the invocations of the optional store that this repo
// must never tell a reader to run unconditionally.
var handoffSubcommands = []string{"handoff create", "handoff show"}

// handoffAllowlist maps a file (relative to tools/cli/) to the reason its
// static `handoff` mention is correct. A file that becomes unnecessary must be
// removed from here — TestHandoffAllowlistHasNoStaleEntries enforces that, so
// the list cannot quietly grow into a blanket exemption.
var handoffAllowlist = map[string]string{
	// The on-disk pointer format. It is a durable artifact read by the NEXT
	// session on the SAME machine, so it must not vary with store presence:
	// doing so would make identity.md's bytes depend on which box wrote them
	// and break the byte-exact assertions in sayguard, say and claim. The
	// interactive surfaces that READ the pointer are the store-aware ones.
	"internal/identity/mem.go": "frozen on-disk pointer format (see pinHandoffPointer); interactive surfaces are store-aware",
	// Static help text cannot probe PATH, so its only honest form is the
	// recipe PLUS the caveat — enforced separately and more strictly by
	// TestHelpNamingHandoffCreateAlsoCarriesTheNotInstalledCaveat.
	"internal/help/help.go": "static help; gated by internal/help's own caveat test",

	// Unreachable without the store, so the static mention is correct rather
	// than merely convenient: WarnIfUnsubmittedHandoff can only return a row
	// the store itself produced (DetectUnsubmittedHandoff -> resolveRow ->
	// runStore shells out to the store binary), so reaching the `handoff show`
	// line proves the command is on PATH. A store-conditional here would be
	// dead code.
	"internal/sayguard/sayguard.go": "the warning can only fire when the store answered, so `handoff show` is resolvable by construction",
}

// walkInternalGoFiles returns every non-test .go file under internal/,
// relative to tools/cli/, sorted by the walk.
func walkInternalGoFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.Walk("internal", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		out = append(out, filepath.ToSlash(path))
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("walkInternalGoFiles found no Go files — this gate would pass vacuously")
	}
	return out
}

// handoffMentions finds every top-level declaration (func, var, const) whose
// body contains a string literal naming a `handoff` subcommand, and reports
// whether that SAME declaration references resolvehandoff.StoreAvailable.
//
// Granularity matters: a file-level check is too coarse to be a gate, because
// once any function in a file is store-aware every other function in it is
// silently exempt — a new `handoff create` in claimBrief would pass just
// because claimNoWorkBrief was fixed. Comments are not string literals, so a
// declaration that only documents the store in prose is never flagged.
func handoffMentions(t *testing.T, path string) (unaware []string, checked int) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	// Names a `handoff` subcommand in any string literal in this node.
	mentions := func(n ast.Node) bool {
		found := false
		ast.Inspect(n, func(inner ast.Node) bool {
			lit, ok := inner.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, sub := range handoffSubcommands {
				if strings.Contains(val, sub) {
					found = true
					return false
				}
			}
			return true
		})
		return found
	}
	// References the store-availability probe in this node.
	probes := func(n ast.Node) bool {
		found := false
		ast.Inspect(n, func(inner ast.Node) bool {
			sel, ok := inner.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "resolvehandoff" && sel.Sel.Name == "StoreAvailable" {
				found = true
				return false
			}
			return true
		})
		return found
	}

	for _, decl := range file.Decls {
		var name string
		var body ast.Node
		switch d := decl.(type) {
		case *ast.FuncDecl:
			name, body = d.Name.Name, d
		case *ast.GenDecl:
			// A grouped var/const block can hold several specs; name it by the
			// first spec so the failure message is still locatable.
			if len(d.Specs) == 0 {
				continue
			}
			spec, ok := d.Specs[0].(*ast.ValueSpec)
			if !ok || len(spec.Names) == 0 {
				continue
			}
			name, body = spec.Names[0].Name, d
		default:
			continue
		}
		if !mentions(body) {
			continue
		}
		checked++
		if !probes(body) {
			unaware = append(unaware, name)
		}
	}
	return unaware, checked
}

func TestRuntimeHandoffPrescriptionsAreStoreAware(t *testing.T) {
	files := walkInternalGoFiles(t)
	checked := 0
	for _, path := range files {
		if _, ok := handoffAllowlist[path]; ok {
			continue
		}
		unaware, n := handoffMentions(t, path)
		checked += n
		for _, name := range unaware {
			t.Errorf("%s: %s names a `handoff` subcommand but never branches on\n"+
				"resolvehandoff.StoreAvailable — `handoff` ships with the author's federation,\n"+
				"not with this repo, so on a plain clone that reader has no such command. Fix\n"+
				"the string, or add the file to handoffAllowlist with a reason for why a\n"+
				"static mention is correct there.", path, name)
		}
	}
	if checked == 0 {
		t.Fatal("no runtime declaration names a handoff subcommand — either the strings moved or this gate is stale")
	}
}

// An allowlist entry that no longer has anything to allow is a silent blanket
// exemption: the next edit to that file stops being checked without anyone
// noticing. Entries must also state a non-empty reason.
func TestHandoffAllowlistHasNoStaleEntries(t *testing.T) {
	files := map[string]bool{}
	for _, f := range walkInternalGoFiles(t) {
		files[f] = true
	}
	for path, reason := range handoffAllowlist {
		if reason == "" {
			t.Errorf("handoffAllowlist[%q] has no reason; every exemption must say why a static mention is correct", path)
		}
		if !files[path] {
			t.Errorf("handoffAllowlist lists %q, which is not a non-test file under internal/ — remove the stale entry", path)
			continue
		}
		if _, n := handoffMentions(t, path); n == 0 {
			t.Errorf("handoffAllowlist lists %q, but no declaration there names a handoff subcommand any more — remove the stale entry", path)
		}
	}
}
