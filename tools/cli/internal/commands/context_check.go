// parlay context-check — machine-readable rotation advisory. Half of the
// Mayor auto-rotation loop (task-gbs). When a persistent agent's context
// window approaches exhaustion (~85%), it should write a handoff and exit
// cleanly so the supervisor respawns a fresh one. This verb is a pure
// DECISION function: the caller passes the context percentage its harness
// knows, and this prints a one-line verdict plus an exit code a script can
// branch on.
//
//	below threshold  → "OK <pct>% (threshold <t>%)"                         exit 0
//	at/above         → "ROTATE: create handoff now, then identity --submit …" exit 3
//	unparseable pct  → usage error                                          exit 2
//
// The ROTATE line's next step is store-aware: `handoff create` is a
// beads-store wrapper from the author's federation, not a command this repo
// installs, so on a plain clone the verdict names the portable substitute
// instead (same treatment as identity --submit and `parlay drawdown`).
//
// Ported from packages/cli/src/commands-context-check.ts.
package commands

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/resolvehandoff"
)

// ExitRotate is deliberately distinct from 0 (ok) / 1 (runtime) / 2 (usage)
// so a scripted caller can `case $? in 3) rotate;; esac` without parsing text.
const ExitRotate = 3

// DefaultRotateThreshold is the rotate-at percentage when --threshold isn't given.
const DefaultRotateThreshold = 85.0

// ParsePercent parses a context-percentage token. Accepts "85", "85%",
// "85.4", "0.85" (a fraction ≤ 1 is scaled to a percent). Returns ok=false
// for anything non-numeric or out of 0–100.
func ParsePercent(raw string) (pct float64, ok bool) {
	cleaned := strings.TrimSuffix(strings.TrimSpace(raw), "%")
	if cleaned == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(cleaned, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	pct = n
	if n > 0 && n <= 1 {
		pct = n * 100 // fraction form (0.85) → 85
	}
	if pct > 100 {
		return 0, false
	}
	return pct, true
}

// RotateVerdict is the pure verdict: does pct meet/exceed threshold?
// Returned as a struct so the decision is unit-testable without touching
// stdout / process exit.
type RotateVerdict struct {
	Rotate   bool
	Line     string
	ExitCode int
}

func formatPercent(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// rotateNextStep is the clause the ROTATE line tells the reader to do next.
// Two stores' worth of truth, chosen by whether the `handoff` store wrapper is
// resolvable — a PATH fact the caller supplies so this stays a pure decision
// function (unit-testable without a subprocess).
func rotateNextStep(storeAvailable bool) string {
	if storeAvailable {
		return "create handoff now, then identity --submit"
	}
	// No store: `handoff create` would be a command that does not exist. The
	// portable rotation is still two steps — write the handoff body (drawdown
	// drafts it from history) and pin it — so name both.
	return "write the handoff body (parlay drawdown drafts one), then pin any id: parlay identity --submit <handoff-id>"
}

// ComputeRotateVerdict decides ROTATE vs OK for pct (rounded to one decimal)
// against threshold. storeAvailable reports whether the `handoff` store CLI is
// on PATH; it only changes the ROTATE line's next-step wording, never the
// verdict itself.
func ComputeRotateVerdict(pct, threshold float64, storeAvailable bool) RotateVerdict {
	p := math.Round(pct*10) / 10
	if p >= threshold {
		return RotateVerdict{
			Rotate:   true,
			Line:     fmt.Sprintf("ROTATE: %s (context %s%% ≥ %s%%)", rotateNextStep(storeAvailable), formatPercent(p), formatPercent(threshold)),
			ExitCode: ExitRotate,
		}
	}
	return RotateVerdict{
		Rotate:   false,
		Line:     fmt.Sprintf("OK %s%% (threshold %s%%)", formatPercent(p), formatPercent(threshold)),
		ExitCode: config.ExitOK,
	}
}

// ContextCheck ports cmdContextCheck.
func ContextCheck(argv []string) {
	if helpWanted("context-check", argv) {
		return
	}
	r := args.Parse("context-check", argv, nil, []string{"--threshold"})

	raw := ""
	if len(r.Positionals) > 0 {
		raw = r.Positionals[0]
	}
	pct, ok := ParsePercent(raw)
	if !ok {
		httpc.Die(
			"parlay context-check: need a context percentage (0–100), e.g. context-check 85 — "+
				"pass what your harness knows; accepts 85, 85%, or 0.85",
			config.ExitUsage,
		)
		return
	}

	threshold := DefaultRotateThreshold
	if v, present := r.String("--threshold"); present {
		t, ok := ParsePercent(v)
		if !ok {
			httpc.Die("parlay context-check: --threshold must be a percentage (0–100)", config.ExitUsage)
			return
		}
		threshold = t
	}

	verdict := ComputeRotateVerdict(pct, threshold, resolvehandoff.StoreAvailable(""))
	fmt.Println(verdict.Line)
	if verdict.ExitCode != config.ExitOK {
		httpc.Exit(verdict.ExitCode)
	}
}
