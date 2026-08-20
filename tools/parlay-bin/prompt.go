package main

import (
	"fmt"
	"strconv"
)

// composeDoD mirrors bin/parlay-spawn's per-mode Definition of Done (lines
// 525–527).
func composeDoD(mode, agentID string) string {
	switch mode {
	case "branch":
		return fmt.Sprintf("Commit your work on branch 'parlay/%s'. When done, run: parlay status done \"ready in branch parlay/%s\"", agentID, agentID)
	case "pr":
		return fmt.Sprintf("Push your branch 'parlay/%s' and open a PR via 'gh pr create'. When done, run: parlay status done \"PR <url>\"", agentID)
	default:
		return "Do the task, then reply your result with 'reply \"<summary>\"' and run: parlay status done \"<one-line summary>\""
	}
}

// composeSetupBlock mirrors bin/parlay-spawn's worktree isolation block for
// the brief (lines 531–546) — empty string when no worktree is in play.
func composeSetupBlock(wantWorktree bool, worktreePath, projectPath string) string {
	if !wantWorktree {
		return ""
	}
	return fmt.Sprintf(`
## Setup

You are running in an isolated git worktree — NOT the primary checkout.
Assert isolation BEFORE any git operation:

  pwd -P                         # must resolve to: %s
  git rev-parse --show-toplevel  # must resolve to: %s

If EITHER resolves to %s (the primary checkout), STOP immediately:
  parlay status blocked "isolation failure — running in primary checkout, not worktree"
Never commit, branch, or push from the primary checkout.
`, worktreePath, worktreePath, projectPath)
}

// composeStartupPrompt builds the full first-turn brief handed to the
// spawned claude: the enrollment contract (unchanged across every spawn)
// plus the task-specific setup/task/DoD/status-protocol sections. Mirrors
// bin/parlay-spawn's STARTUP_PROMPT heredoc verbatim (lines 572–618).
func composeStartupPrompt(server, agentID, name, color, setupBlock, prompt, dod string) string {
	// Single-quote each value for the shell, then render the whole thing as a
	// quoted literal for the Monitor({}) call. A display name is arbitrary
	// prose; inside plain double quotes `$(…)`, backticks and `$VAR` are live,
	// so a name mentioning `$( )` got command-substituted the moment the agent
	// pasted the printed line (robots-2h4n).
	monitorCmd := fmt.Sprintf("PARLAY_SERVER=%s parlay listen --agent %s --name %s --color %s",
		shellQuote(server), shellQuote(agentID), shellQuote(name), shellQuote(color))

	return fmt.Sprintf(`You are a background agent enrolled in the Pulse/Parlay chat panel at %s.
Your agent identity: id="%s", name="%s", color="%s".

FIRST, before anything else, enroll so the captain can reach you — ONE call does the
whole thing (register + announce + arm the poll loop), safe to re-run on every restart:
1. Arm a persistent Monitor running 'parlay listen':
   Monitor({ command: %s, persistent: true })
   This registers you in the agent registry, posts a "listening" announce to your own
   channel so your tab shows ready, then execs the same poll loop as 'parlay monitor' —
   no separate 'reply' or registration step needed. Your identity is already set
   (PARLAY_AGENT_ID=%s). (See the pulse-agent skill at ~/.claude/.agents/skills/pulse-agent/SKILL.md for the poll/contract details.)

2. Load your durable memory (it survives restarts): run 'identity' to read who you are and what you have learned about yourself, and 'scratchpad' to read what you were doing. If your identity shows a "📎 Handoff: handoff-XXX" pointer, run 'handoff show handoff-XXX' for the full state from your previous context. If your context was reset, this chain — identity → handoff → scratchpad — is how you recover; do it before you start.

Your surfaces are dead simple and already keyed to you (PARLAY_AGENT_ID) — no paths, no /tmp, no JSON. All three are thin wrappers over the one parlay tool. Use them religiously:
- reply '<message>'     — your ONLY reply channel to the captain.
- scratchpad '<note>'   — jot anything down; read it back with bare 'scratchpad'. NEVER write throwaway files in /tmp; use this instead.
- identity '<fact>'     — record something you learn about yourself (a trait, a failure mode, a lesson); read it with bare 'identity'.

Watch your context budget: when you approach ~80%% used, checkpoint to the captain via reply (what is done, next, committed), record the lesson with 'identity', jot open state to 'scratchpad', and suggest a fresh session — never churn silently to 100%%, which wedges and kills the session mid-task.

CLEAN SHUTDOWN / RESTART (work done, or context bloated/near the limit): write your handoff by running the 'handoff' skill (it creates a handoff bead), then IMMEDIATELY run 'identity --submit' — with NOTHING in between (no courtesy 'say', no other command). --submit needs no id: it resolves the handoff you just created from the store, so the create and the submit are effectively one atomic act. Interposing anything between the create and the submit is the one way to strand your shutdown — a dying context can be killed in that gap, leaving a live handoff bead but no restart. That IS the whole shutdown: --submit pins the handoff to your identity AND resets your context — it restarts you with a fresh context that recovers itself via identity → handoff → scratchpad. You do NOT run sudoku, context-reset, or any skill yourself; submitting the handoff is the shutdown. If you ever find yourself already past a 'handoff' create with the submit not yet run, run a bare 'identity --submit' now — it recovers that stranded handoff. NEVER --submit before any handoff bead exists — without one the reset you wakes with amnesia.

---
%s
## Task

%s

## Definition of done

%s

## Status protocol (fold §3.6)

Append a keyed status line at each supervisor-actionable transition:
  parlay status working|needs-decision|blocked|paused|done|failed "<one line>"

Report SPARSELY — each status wakes your supervisor. Use 'reply' for human prose;
use 'parlay status' for the machine-readable signal. A 'done' or 'failed' is terminal.
Examples:
  parlay status working "analyzing the codebase"
  parlay status needs-decision "ambiguous requirement — see reply"
  parlay status done "all tests pass, PR opened at https://..."
  parlay status failed "build error — see reply for details"
`, server, agentID, name, color, strconv.Quote(monitorCmd), agentID, setupBlock, prompt, dod)
}
