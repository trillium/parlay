package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/help"
)

// The README tells a newcomer `./bin/parlay help  # every verb`, and the usage
// text closes with "Any subcommand accepts --help". Both claims are only true
// while the dispatcher's verb set and internal/help stay in step, and nothing
// enforced that: `drawdown`, `idle`, `spawn-account` and `inbox-dispatch` were
// all dispatchable, all absent from `parlay help`, and all answered `--help`
// by dumping the 90-line USAGE — which for the first three never mentions the
// verb the reader typed. The fix is only durable if the next verb added to
// dispatch cannot skip this, which is what these two tests are for.
//
// The verb list is read out of main.go's own switch rather than duplicated
// here, because a hand-maintained copy would drift the moment someone adds a
// verb — the exact failure this test exists to catch.

// dispatchedVerbs parses this package's main.go and returns every string the
// dispatch switch routes, sorted. It fails the test (rather than returning an
// empty list) if main.go cannot be parsed or has no dispatch switch, so a
// refactor that moves the dispatcher cannot turn these tests vacuously green.
func dispatchedVerbs(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}

	var verbs []string
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "dispatch" {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			sw, ok := inner.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			for _, stmt := range sw.Body.List {
				clause, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range clause.List {
					lit, ok := expr.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					s, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("unquoting dispatch case %s: %v", lit.Value, err)
					}
					if s == "" {
						// `case "":` is the bare `parlay` snapshot, not a verb.
						// It is documented by the usage line that has no name
						// after `parlay`, so there is nothing here to name.
						continue
					}
					verbs = append(verbs, s)
				}
			}
			return false
		})
		return false
	})

	if len(verbs) == 0 {
		t.Fatal("no case labels found in dispatch() — the dispatcher moved or changed shape, " +
			"so this file's coverage check is now vacuous; repoint it at the new switch")
	}
	sort.Strings(verbs)
	return verbs
}

// usageText is the rendered USAGE for a throwaway server URL, matching what
// `parlay help` prints.
func usageText() string {
	return help.Usage("http://coverage.test:4242")
}

// TestEveryDispatchedVerbIsInHelpUsage pins the README's "every verb" claim
// for the top-level listing. It is substring-based on purpose: a verb counts
// as documented when its name appears anywhere in USAGE, so aliases
// ("alias: agent-up"), grouped families ("route decide", "variant launch") and
// spellings in prose all satisfy it — which is exactly the bar a reader has to
// clear to find the verb at all.
func TestEveryDispatchedVerbIsInHelpUsage(t *testing.T) {
	usage := usageText()
	for _, verb := range dispatchedVerbs(t) {
		if !strings.Contains(usage, verb) {
			t.Errorf("parlay %s is dispatchable but `parlay help` never mentions it — "+
				"add a line to the usage table in internal/help/help.go", verb)
		}
	}
}

// selfDescribingVerbs are the dispatched verbs that deliberately carry their
// own usage text instead of an entry in help.HELP, so each one is exempt here
// and pinned by a test in its own package instead.
//
// The subprocess family is the real case: one const covers spawn/stop/ping,
// and internal/spawn cannot import internal/help without moving that text.
// The other three are the top-level help forms, which print USAGE by design.
// Dispatch aliases are NOT exempt — help.Lookup resolves them (see
// help.Aliases), so `parlay reply --help` answers with say's text.
var selfDescribingVerbs = map[string]string{
	"subprocess-spawn": "internal/spawn/subprocess_spawn.go's own usage const (subprocessHelpWanted)",
	"subprocess-stop":  "same const — see internal/spawn's TestSubprocessHelpIsAnsweredNotExecuted",
	"subprocess-ping":  "same const — see internal/spawn's TestSubprocessHelpIsAnsweredNotExecuted",
	"help":             "prints the full USAGE by definition",
	"--help":           "top-level alias for 'help'",
	"-h":               "top-level alias for 'help'",
}

// TestEveryDispatchedVerbAnswersHelp pins the "Any subcommand accepts --help"
// claim: a verb whose name resolves to nothing in HELP makes helpWanted fall
// back to dumping the whole USAGE, which is the shape that hid four verbs from
// a reader who asked for exactly one of them.
func TestEveryDispatchedVerbAnswersHelp(t *testing.T) {
	for _, verb := range dispatchedVerbs(t) {
		if _, exempt := selfDescribingVerbs[verb]; exempt {
			continue
		}
		if _, ok := help.Lookup(verb); !ok {
			t.Errorf("parlay %s --help has no per-command help text, so it prints the full "+
				"USAGE instead — add a HELP entry in internal/help/help.go, or record why it "+
				"is exempt in selfDescribingVerbs", verb)
		}
	}
}

// TestHelpAliasesPointAtRealVerbs keeps help.Aliases honest: an alias whose
// canonical target has no help text would resolve to nothing, which is the
// same silent-fallback this file exists to prevent — just reached through the
// alias map instead of the dispatcher.
func TestHelpAliasesPointAtRealVerbs(t *testing.T) {
	dispatched := map[string]bool{}
	for _, verb := range dispatchedVerbs(t) {
		dispatched[verb] = true
	}
	for alias, canonical := range help.Aliases {
		if !dispatched[alias] {
			t.Errorf("help.Aliases lists %q, which dispatch() does not route — "+
				"an alias nothing dispatches is a lie to a reader", alias)
		}
		if !dispatched[canonical] {
			t.Errorf("help.Aliases maps %q to %q, which dispatch() does not route", alias, canonical)
		}
		if _, ok := help.Lookup(canonical); !ok {
			t.Errorf("help.Aliases maps %q to %q, which has no HELP entry — so `%s --help` "+
				"still dumps the full USAGE", alias, canonical, alias)
		}
		if alias == canonical {
			t.Errorf("help.Aliases maps %q to itself; an alias must name a different verb", alias)
		}
	}
}

// TestSelfDescribingExemptionsAreAllReal keeps the exemption list from
// becoming a place where verbs quietly go to die: every entry must still be
// dispatched (a renamed verb leaves a stale exemption), and every non-help
// exemption must carry a reason.
func TestSelfDescribingExemptionsAreAllReal(t *testing.T) {
	dispatched := map[string]bool{}
	for _, verb := range dispatchedVerbs(t) {
		dispatched[verb] = true
	}
	for verb, reason := range selfDescribingVerbs {
		if !dispatched[verb] {
			t.Errorf("selfDescribingVerbs lists %q, which dispatch() no longer routes — "+
				"remove the stale exemption", verb)
		}
		if reason == "" {
			t.Errorf("selfDescribingVerbs[%q] has no reason; every exemption must say who "+
				"answers --help for it", verb)
		}
	}
}
