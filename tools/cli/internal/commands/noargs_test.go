package commands

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// The bug this file exists to prevent, stated once:
//
// PR #115 fixed `parlay lavish-import --dry-run`, which accepted the flag,
// dropped it, and performed a REAL import into the live Parlay at :31337. The
// flag was never read. Nothing rejected it. AGENTS.md's rule — a dropped flag
// is not a degraded flag, it is a hard exit — exists because of that incident.
//
// task-bbp6b then filed `parlay stats` as the same shape, found by sweeping for
// a verb whose body mentions `argv` exactly twice. That heuristic was too
// narrow: it missed `Health` and `Doctor`, which live together in doctor.go and
// so mention `argv` four times between them. Fixing only the ticketed verb
// would have left two live instances of the exact class the ticket was about.
//
// So the check below is not three point-tests. It derives the list of verbs
// from the source and fails on any one that neither reads argv nor rejects it —
// including verbs that do not exist yet. A hand-kept list of "verbs that take no
// arguments" would rot the same way the sweep's heuristic did.

// TestEveryVerbEitherReadsArgvOrRejectsIt walks this package's AST and fails
// naming any command function that accepts argv, never looks at it beyond the
// helpWanted call, and does not call rejectExtraArgs.
//
// Those three states are exhaustive for a verb that takes `argv []string`:
// it uses the arguments, it refuses them, or it silently discards them. Only
// the third is a defect, and it is invisible at every layer above — the caller
// gets exit 0 and a plausible-looking result.
func TestEveryVerbEitherReadsArgvOrRejectsIt(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || !takesArgv(fn) {
				continue
			}
			checked++
			reads, rejects := argvUsage(fn)
			if reads || rejects {
				continue
			}
			t.Errorf("%s: %s(argv []string) accepts arguments and neither reads nor rejects them.\n"+
				"  Every flag or positional a caller passes is silently discarded, and the verb exits 0\n"+
				"  as if it had honoured them — the PR #115 shape. Add `if rejectExtraArgs(%q, argv) { return }`\n"+
				"  directly after the helpWanted call, or make the verb actually parse argv.",
				filepath.Join("internal/commands", name), fn.Name.Name, strings.ToLower(fn.Name.Name))
		}
	}

	// A refactor that renamed the parameter, or moved every verb to another
	// package, would make the loop above vacuously pass. Assert it had
	// something to look at — the count is deliberately a floor, not an
	// equality, so adding a verb does not fail this test spuriously.
	if checked < 5 {
		t.Fatalf("only %d argv-taking functions found in internal/commands — the AST walk is "+
			"probably no longer looking at the verbs, so this check is passing vacuously", checked)
	}
}

// takesArgv reports whether fn's signature has a parameter named argv of type
// []string. Matching the name as well as the type keeps this from firing on
// unrelated string-slice parameters.
func takesArgv(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		arr, ok := field.Type.(*ast.ArrayType)
		if !ok || arr.Len != nil {
			continue
		}
		if ident, ok := arr.Elt.(*ast.Ident); !ok || ident.Name != "string" {
			continue
		}
		for _, n := range field.Names {
			if n.Name == "argv" {
				return true
			}
		}
	}
	return false
}

// argvUsage reports whether fn's body reads argv for its own purposes, and
// whether it calls rejectExtraArgs.
//
// Passing argv to helpWanted does NOT count as reading it: every one of these
// verbs does that, and it is exactly the line present in the lavish-import bug.
// It answers "did the caller ask for help", not "what did the caller ask for".
func argvUsage(fn *ast.FuncDecl) (reads, rejects bool) {
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		switch ident.Name {
		case "rejectExtraArgs":
			rejects = true
			return false
		case "helpWanted":
			// Skip the whole call, argv argument included.
			return false
		}
		return true
	})

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok {
				if ident.Name == "helpWanted" || ident.Name == "rejectExtraArgs" {
					return false
				}
			}
		}
		if ident, ok := n.(*ast.Ident); ok && ident.Name == "argv" {
			reads = true
		}
		return true
	})
	return reads, rejects
}

// TestRejectExtraArgsExitsUsageForEveryArgumentShape pins the behaviour the AST
// check above only asserts is wired up. A caller's argument can arrive as a
// long flag, a short flag, a flag with a value, or a bare positional; all four
// are equally "this verb has no idea what you asked for".
func TestRejectExtraArgsExitsUsageForEveryArgumentShape(t *testing.T) {
	origDie := httpc.Exit
	t.Cleanup(func() { httpc.Exit = origDie })
	httpc.Exit = testsupport.RecordingExit()

	shapes := [][]string{
		{"--json"},
		{"-v"},
		{"--since", "1h"},
		{"yesterday"},
		{"--dry-run"}, // the PR #115 flag, by name
	}
	for _, argv := range shapes {
		t.Run(strings.Join(argv, "_"), func(t *testing.T) {
			code, exited := testsupport.Capture(func() { rejectExtraArgs("stats", argv) })
			if !exited {
				t.Fatalf("rejectExtraArgs(%q) returned normally — the argument was accepted and dropped", argv)
			}
			if code != config.ExitUsage {
				t.Fatalf("rejectExtraArgs(%q) exited %d, want ExitUsage (%d)", argv, code, config.ExitUsage)
			}
		})
	}
}

// TestRejectExtraArgsPassesAnEmptyArgv is the no-regression half: the guard
// must not turn every argument-free invocation into an error.
func TestRejectExtraArgsPassesAnEmptyArgv(t *testing.T) {
	origDie := httpc.Exit
	t.Cleanup(func() { httpc.Exit = origDie })
	httpc.Exit = testsupport.RecordingExit()

	for _, argv := range [][]string{nil, {}} {
		code, exited := testsupport.Capture(func() {
			if rejectExtraArgs("stats", argv) {
				t.Errorf("rejectExtraArgs reported it had exited on argv %#v", argv)
			}
		})
		if exited {
			t.Fatalf("rejectExtraArgs(%#v) exited %d — a bare `parlay stats` must still run", argv, code)
		}
	}
}

// TestHelpStillWinsOverTheArgumentGuard is the ordering pin, and it has to be
// an AST check rather than a behavioural one: `--help` IS an unexpected
// argument by rejectExtraArgs's definition, so a verb that called the guard
// first would answer `parlay stats --help` with "unexpected argument
// \"--help\"" instead of printing the help text.
//
// Calling helpWanted and rejectExtraArgs directly in a test would prove
// nothing about that, because the defect lives entirely in the order of two
// lines inside each verb. So this walks the same source the guard check walks
// and compares the two calls' positions in every function that has both.
func TestHelpStillWinsOverTheArgumentGuard(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	pairs := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			help, guard := callPos(fn, "helpWanted"), callPos(fn, "rejectExtraArgs")
			if help == token.NoPos || guard == token.NoPos {
				continue
			}
			pairs++
			if help < guard {
				continue
			}
			t.Errorf("%s: %s calls rejectExtraArgs (line %d) before helpWanted (line %d).\n"+
				"  --help is an unexpected argument as far as the guard is concerned, so this makes\n"+
				"  `parlay %s --help` fail with a usage error instead of printing its help text.",
				filepath.Join("internal/commands", name), fn.Name.Name,
				fset.Position(guard).Line, fset.Position(help).Line,
				strings.ToLower(fn.Name.Name))
		}
	}

	if pairs == 0 {
		t.Fatal("no function calls both helpWanted and rejectExtraArgs — this ordering check " +
			"is passing vacuously, so it would not notice a reordered call site")
	}
}

// callPos returns the position of the first call to the named package-level
// function inside fn, or token.NoPos if there is none.
func callPos(fn *ast.FuncDecl, name string) token.Pos {
	pos := token.NoPos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if pos != token.NoPos {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == name {
			pos = call.Pos()
			return false
		}
		return true
	})
	return pos
}
