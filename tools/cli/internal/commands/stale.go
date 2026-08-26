// parlay stale — the stale-window detector (robots-9d2w).
//
// The defect this exists to fix: a pane that FINISHED its task is
// indistinguishable, to a sender, from one that is still working. Both are
// registered, both are enrolled with the relay, both accept a message. So a
// re-task lands in the finished pane and the new work is done on top of the
// old session's whole transcript — the captain caught one at 70% context with
// its own harness offering "new task? /clear to save 141.4k tokens" directly
// under the summary of the job it had just closed out. Every turn of the new
// task re-pays for 141.4k tokens of a job that is already merged. His note:
// "a message would waste tokens, so this pane should be relaunched, rather
// than continued. We need a detector, this is a stale window."
//
// # What makes a window stale
//
// A terminal status verb on an ENROLLED agent. `done`/`failed` both mean the
// agent stopped working and is sitting at its prompt — the exact shape above.
// Everything else is deliberately NOT stale, and each exclusion is load-bearing:
//
//   - needs-decision / blocked / paused are agents WAITING ON A REPLY. A
//     message is the intended way to unblock them; refusing that send would
//     break the fleet's main steering path to fix a token leak.
//   - working is obvious.
//   - unknown is the fail-open case. CrewStateForAgent returns unknown when
//     the relay is unreachable or nothing was ever recorded, so treating it as
//     stale would turn every relay hiccup into a refused send — trading a
//     token leak for a lost message, which is the worse failure (robots-ngg5).
//   - a keep-listed id ($PARLAY_STATE_HOME/sweep-keep) is never stale. That
//     list already exists for sweep as "the escape hatch for long-lived named
//     agents like a dispatcher that legitimately sits at `done` between jobs"
//     (robots-6xq7) — an agent designed to be re-tasked in place is precisely
//     one whose `done` must NOT read as a spent window. One list, both verbs.
//
// # Why a verb and not just a check inside send
//
// `parlay send` refuses a stale target (see refuseStaleWindow in send.go), but
// send is not the only way a pane gets continued: firstmate's fm-send.sh types
// into it, the Pulse panel posts to /api/chat/send directly, and robots-watch
// dispatches re-tasks. Those callers need to ask the same question without
// re-deriving the policy — so the policy is a pure function (ClassifyStaleWindow),
// the verb is a thin script-callable wrapper over it, and exit 3 is the
// branchable "relaunch instead" signal. Exit 3 matches context-check's ROTATE:
// same meaning from the other side of the pane (context-check is the agent
// deciding to rotate itself; stale is a sender discovering it should have).
//
// The verb is read-only. It never tears down, never spawns, never sends —
// closing a stale window destroys a store, and that decision belongs to
// `parlay sweep --apply` (which asks the same question with the same holds),
// not to a detector something might call in a loop.
//
// Go-only, no TS port — same call as merge_gate.go and sweep.go. bin/parlay
// now execs the Go binary for every verb; the TS CLI and the parity harness
// that diffed against it were both retired in T-08.
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/identity"
)

// ExitStale is the branchable "this window is spent — relaunch, do not
// continue" signal. Deliberately 3, matching context-check's ExitRotate: the
// two verbs answer the same question (this session should be replaced by a
// fresh one) from opposite ends, so a caller that already branches on 3 for
// rotation branches correctly here too.
const ExitStale = ExitRotate

// staleTerminalVerbs are the crew states that mean "this agent finished and is
// sitting at its prompt". Deliberately narrower than sweep's terminal set:
// needs-decision/blocked are terminal for the SWEEP (they must not be absorbed)
// but are emphatically continuable for a SENDER — they are waiting to be
// answered. See the package doc.
var staleTerminalVerbs = map[string]bool{
	"done":   true,
	"failed": true,
}

// StaleWindow is one target, already resolved from disk + the relay. Kept as
// plain data so ClassifyStaleWindow stays pure.
type StaleWindow struct {
	ID string
	// State/Detail/Source come from CrewStateForAgent. Source is "status" when
	// the relay confirmed enrollment, "status-degraded" when the relay was
	// unreachable, or "status-unenrolled" when the relay disagreed.
	State  string
	Detail string
	Source string
	// Keep is whether the id is on the sweep-keep list — a long-lived agent
	// that is re-tasked in place and therefore never a spent window.
	Keep bool
	// SessionAge is how long this pane has been up, from its store's
	// session-start stamp. Zero when unknown. Reported, never decisive: age
	// alone cannot tell a six-hour working agent from a spent one, and a
	// five-minute agent that posted `done` is already stale.
	SessionAge time.Duration
}

// StaleVerdict is one classified window: the answer, why, and the exit code a
// script branches on.
type StaleVerdict struct {
	ID       string
	Stale    bool
	Reason   string
	ExitCode int
}

// ClassifyStaleWindow applies the staleness policy to one target. Pure and
// total: no I/O, and every input shape yields a verdict.
func ClassifyStaleWindow(w StaleWindow) StaleVerdict {
	verdict := func(stale bool, reason string) StaleVerdict {
		code := config.ExitOK
		if stale {
			code = ExitStale
		}
		return StaleVerdict{ID: w.ID, Stale: stale, Reason: reason, ExitCode: code}
	}

	if w.Keep {
		return verdict(false, "keep-listed (sweep-keep) — re-tasked in place by design")
	}
	// Relay unreachable: status may be stale — fail open rather than blocking a
	// send on unconfirmed data. Same policy as unknown (robots-ngg5).
	if w.Source == "status-degraded" {
		return verdict(false, fmt.Sprintf("state=%s — %s; relay unreachable, so not provably stale", w.State, w.Detail))
	}
	if !staleTerminalVerbs[w.State] {
		// Names the state so a caller can tell "still working" from "waiting
		// on you" from "the relay could not answer" without a second call.
		switch w.State {
		case "needs-decision", "blocked":
			return verdict(false, fmt.Sprintf("state=%s — waiting on a reply; a message is the intended unblock", w.State))
		case "unknown":
			return verdict(false, fmt.Sprintf("state=unknown — %s; not provably spent, so not stale", w.Detail))
		default:
			return verdict(false, fmt.Sprintf("state=%s", w.State))
		}
	}

	reason := fmt.Sprintf("state=%s — finished and sitting at its prompt", w.State)
	if w.SessionAge > 0 {
		reason += fmt.Sprintf("; session up %s", formatSessionAge(w.SessionAge))
	}
	if w.Detail != "" {
		reason += fmt.Sprintf(" (%s)", w.Detail)
	}
	return verdict(true, reason)
}

// formatSessionAge renders a duration the way a human reads a pane's age:
// whole minutes under an hour, one decimal hour above it.
func formatSessionAge(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return strconv.FormatFloat(d.Hours(), 'f', 1, 64) + "h"
}

// readSessionAge reads ~/.parlay/agents/<id>/session-start (a unix timestamp
// written at spawn) and returns how long the pane has been up. Any unreadable
// or unparseable stamp is 0 — this is reporting colour, never a decision
// input, so it must not be able to fail the caller.
func readSessionAge(agentID string) time.Duration {
	data, err := os.ReadFile(filepath.Join(identity.AgentsRoot(), agentID, "session-start"))
	if err != nil {
		return 0
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || secs <= 0 {
		return 0
	}
	age := time.Since(time.Unix(secs, 0))
	if age < 0 {
		return 0
	}
	return age
}

// resolveStaleWindow gathers one target's crew state, keep-list membership,
// and session age.
func resolveStaleWindow(agentID string) StaleWindow {
	st := CrewStateForAgent(agentID)
	return StaleWindow{
		ID:         agentID,
		State:      st.State,
		Detail:     st.Detail,
		Source:     st.Source,
		Keep:       readSweepKeep()[agentID],
		SessionAge: readSessionAge(agentID),
	}
}

// relaunchAdvice is the two-line "what to do instead" every stale refusal
// prints — shared by this verb and send's pre-flight so the remedy can never
// drift between them. Indented to sit under whichever headline precedes it.
func relaunchAdvice(agentID string) string {
	return "  Relaunch instead of continuing:\n" +
		fmt.Sprintf("    parlay sweep --apply --agent %s     # close the finished pane (refuses if it still holds work)\n", agentID) +
		"    parlay-spawn <id> <name> <color> --claim <task-id>   # a fresh pane for the new task\n" +
		fmt.Sprintf("  If this agent is a long-lived dispatcher that IS re-tasked in place, add %s to %s.",
			agentID, filepath.Join(config.StateHome(), "sweep-keep"))
}

// Stale is `parlay stale <agent-id>`'s entry point.
func Stale(argv []string) {
	if helpWanted("stale", argv) {
		return
	}
	r := args.Parse("stale", argv, []string{"--quiet"}, nil)

	agentID := ""
	if len(r.Positionals) > 0 {
		agentID = strings.TrimSpace(r.Positionals[0])
	}
	if agentID == "" {
		httpc.Die("parlay stale: agent id required (e.g. 'parlay stale mc-robots-9d2w')", config.ExitUsage)
		return
	}

	v := ClassifyStaleWindow(resolveStaleWindow(agentID))
	if !r.Bool("--quiet") {
		headline := "FRESH"
		if v.Stale {
			headline = "STALE"
		}
		fmt.Printf("%s %s — %s\n", headline, v.ID, v.Reason)
		if v.Stale {
			fmt.Println(relaunchAdvice(v.ID))
		}
	}
	if v.ExitCode != config.ExitOK {
		httpc.Exit(v.ExitCode)
	}
}
