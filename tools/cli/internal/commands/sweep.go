// parlay sweep — the fleet closure sweep (robots-6xq7).
//
// The defect this exists to fix: parlay had every PIECE of agent closure and
// no COLLECTOR. `parlay crew-state <id>` already reports an agent's terminal
// state correctly, and `parlay teardown <id>` already does the safe destroy
// (git-status check, unpushed-commits-landed validation, relay deregister,
// worktree removal, store delete) — but nothing ever walked the fleet and put
// the two together. `parlay supervise <id>` is per-agent wake-on-status, not a
// sweeper. So agents finished their work, posted `done: PR <url>`, and then
// waited forever for a collector that never came: 38 stale panes accumulated
// against 2 live orchestrators, with 306 ids still in the relay registry.
//
// Firstmate's own staleness auto-close cannot cover them and never could:
// every firstmate shutdown path (bin/fm-teardown.sh, fm-watch.sh's
// recorded_windows() enumeration behind both the idle>2h auto-close and the
// dead-window triage) is keyed on a state/.meta file that a parlay-spawned
// agent has no reason to own. A pane with no .meta is invisible to that
// watcher, permanently. This verb is parlay closing its OWN agents, keyed on
// the parlay agent store (~/.parlay/agents/<id>/), not on firstmate metadata.
//
// # What it will and will not close
//
// Closing an agent deletes its store and drops it from the registry, so the
// bar for acting unattended is high and every rule below fails SAFE — an
// agent that cannot be proven closeable is HELD (surfaced, left alone), never
// swept. ClassifySweep is that policy, kept pure and total so sweep_test.go
// can pin each rule without a filesystem or a relay:
//
//   - the sweeping agent never sweeps itself;
//   - a keep-listed id is never swept ($PARLAY_STATE_HOME/sweep-keep, one id
//     per line, '#' comments — the escape hatch for long-lived named agents
//     like a dispatcher that legitimately sits at `done` between jobs);
//   - an agent with NO frontmatter on disk is HELD even under --all, because
//     an empty launch spec cannot distinguish "no worktree" from "a worktree
//     this CLI failed to record" — exactly the case robots-6xq7's sibling
//     defect created (see internal/identity/store.go's MemValueFlags note),
//     and the one case where teardown's git safety silently does not apply.
//     --force is the deliberate override;
//   - only `done` is closeable. needs-decision/blocked/failed are terminal
//     but captain-relevant, so they are HELD and reported, never absorbed;
//     working/paused and an unknown state are left alone;
//   - a `done` agent is closed unattended only when its store proves it is a
//     per-task spawn: a bound `task:` or a recorded `worktree:`. Anything
//     else (a named, hand-made agent that happens to read `done`) is HELD
//     with a pointer to sweep it explicitly by id.
//
// Acting is opt-in: the default is a dry run that reports the plan. --apply
// performs it, one `teardownAgent` call per agent — the same chain
// `parlay teardown` runs, so a `done` agent whose worktree still holds
// uncommitted or unlanded work is refused there too, and reported as refused
// rather than forced.
//
// Go-only, no TS port — same call as merge_gate.go (bin/parlay execs the Go
// binary for everything except lavish-import; packages/cli is the retired
// path). Do not add it to tools/cli/parity/run.sh; there is no TS side to
// diff against.
package commands

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// Sweep actions. Hold is the load-bearing one: it means "this agent is NOT
// closeable unattended" and is always printed, dry run or not — a held agent
// is the sweep asking for a human, which is the behavior the ticket asks for
// in place of closing something ambiguous.
const (
	SweepTeardown = "teardown"
	SweepHold     = "hold"
	SweepSkip     = "skip"
)

// SweepAgent is one candidate, already resolved from disk + the relay. Kept
// as plain data so ClassifySweep stays pure.
type SweepAgent struct {
	ID string
	// State/Detail come from CrewStateForAgent.
	State  string
	Detail string
	// HasFrontmatter is whether identity.md carried a parseable --- block at
	// all; Task/Worktree are its `task:`/`worktree:` fields.
	HasFrontmatter bool
	Task           string
	Worktree       string
	// NotFound is set when the agent's store directory does not exist at all.
	// Distinct from HasFrontmatter=false, which means the directory exists but
	// identity.md lacks a parseable frontmatter block.
	NotFound bool
}

// SweepOpts is the policy context ClassifySweep decides against.
type SweepOpts struct {
	// Self is the sweeping agent's own id ($PARLAY_AGENT_ID), never swept.
	Self string
	// Keep is the keep-list, by id.
	Keep map[string]bool
	// Explicit is set when the caller named this agent with --agent, which
	// substitutes for the per-task-spawn proof (but not for the keep-list,
	// the self guard, or the empty-frontmatter guard).
	Explicit bool
	// All drops the per-task-spawn proof requirement fleet-wide.
	All bool
	// Force additionally drops the empty-frontmatter guard.
	Force bool
}

// SweepVerdict is one classified agent: what to do and why, in the exact
// words the sweep prints.
type SweepVerdict struct {
	ID     string
	Action string
	Reason string
}

// terminalHoldVerbs are terminal states that are emphatically NOT closeable:
// each one means the agent stopped and wants the captain. Absorbing these
// into a teardown would destroy the very state the captain needs to read.
var terminalHoldVerbs = map[string]bool{
	"needs-decision": true,
	"blocked":        true,
	"failed":         true,
}

// ClassifySweep applies the closure policy to one agent. Pure and total: no
// I/O, and every input shape yields a verdict.
func ClassifySweep(a SweepAgent, o SweepOpts) SweepVerdict {
	verdict := func(action, reason string) SweepVerdict {
		return SweepVerdict{ID: a.ID, Action: action, Reason: reason}
	}

	if a.ID == "" {
		return verdict(SweepSkip, "empty agent id")
	}
	if o.Self != "" && a.ID == o.Self {
		return verdict(SweepSkip, "this is the sweeping agent itself")
	}
	if o.Keep[a.ID] {
		return verdict(SweepSkip, "keep-listed (sweep-keep)")
	}

	// An empty launch spec cannot prove the agent has no worktree, and
	// teardown's git safety only runs on a RECORDED worktree — so tearing
	// this down would delete the store and orphan whatever the worktree
	// holds, unchecked. Hold ahead of the state check so the reason names
	// the real blocker.
	if !a.HasFrontmatter && !o.Force {
		return verdict(SweepHold, "no launch spec on disk — cannot prove it has no worktree; triage by hand (or --force)")
	}

	switch {
	case a.State == "done":
		// closeable — fall through to the per-task-spawn proof below.
	case terminalHoldVerbs[a.State]:
		return verdict(SweepHold, fmt.Sprintf("state=%s — wants the captain, not a teardown: %s", a.State, a.Detail))
	case a.State == "unknown":
		return verdict(SweepHold, fmt.Sprintf("state=unknown — %s", a.Detail))
	default:
		return verdict(SweepSkip, fmt.Sprintf("state=%s", a.State))
	}

	// --force implies the proof waiver too: an agent with no launch spec has
	// no `task:`/`worktree:` by construction, so a --force that still fell
	// through to this hold could never close the very agents it exists to
	// unblock.
	if o.Explicit || o.All || o.Force || a.Task != "" || a.Worktree != "" {
		return verdict(SweepTeardown, sweepDoneReason(a))
	}
	return verdict(SweepHold, "done, but no bound task and no recorded worktree — sweep it explicitly: parlay sweep --apply --agent "+a.ID)
}

// sweepDoneReason names WHY a done agent is closeable, so the log line is
// auditable after the fact rather than a bare "teardown".
func sweepDoneReason(a SweepAgent) string {
	switch {
	case a.Worktree != "":
		return "done · worktree " + a.Worktree
	case a.Task != "":
		return "done · task " + a.Task
	default:
		return "done"
	}
}

// readSweepKeep loads $PARLAY_STATE_HOME/sweep-keep: one agent id per line,
// blank lines and '#' comments ignored. A missing file is an empty list, not
// an error — the keep-list is opt-in.
func readSweepKeep() map[string]bool {
	keep := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(config.StateHome(), "sweep-keep"))
	if err != nil {
		return keep
	}
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		if id := strings.TrimSpace(line); id != "" {
			keep[id] = true
		}
	}
	return keep
}

// sweepCandidates lists every agent store under ~/.parlay/agents, sorted, so
// a sweep pass is deterministic and reproducible from its own log.
func sweepCandidates() []string {
	entries, err := os.ReadDir(parlayAgentsDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "sweep: cannot read agents dir: %v\n", err)
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// resolveSweepAgent reads one candidate's on-disk launch spec and its
// reconciled crew state.
func resolveSweepAgent(id string) SweepAgent {
	dir := filepath.Join(parlayAgentsDir(), id)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return SweepAgent{ID: id, NotFound: true}
	}
	fm := readLocalFrontmatter(filepath.Join(dir, "identity.md"))
	st := CrewStateForAgent(id)
	return SweepAgent{
		ID:             id,
		State:          st.State,
		Detail:         st.Detail,
		HasFrontmatter: len(fm.keys) > 0,
		Task:           fm.Get("task"),
		Worktree:       fm.Get("worktree"),
	}
}

// Sweep is `parlay sweep`'s entry point.
func Sweep(argv []string) {
	if helpWanted("sweep", argv) {
		return
	}
	r := args.Parse("sweep", argv, []string{"--apply", "--all", "--force", "--verbose"}, []string{"--agent", "--interval"})

	apply := r.Bool("--apply")
	explicitID := ""
	if v, present := r.String("--agent"); present {
		explicitID = strings.TrimSpace(v)
		if explicitID == "" {
			httpc.Die("parlay sweep: --agent requires an agent id", config.ExitUsage)
			return
		}
	}

	intervalSec := 0.0
	if raw, present := r.String("--interval"); present {
		raw = strings.TrimSpace(raw)
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
			httpc.Die(fmt.Sprintf("parlay sweep: --interval must be a positive number of seconds (got '%s')", raw), config.ExitUsage)
			return
		}
		if explicitID != "" {
			httpc.Die("parlay sweep: --interval sweeps the fleet on a loop; it cannot be combined with --agent", config.ExitUsage)
			return
		}
		intervalSec = n
	}

	opts := SweepOpts{
		Self:     strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID")),
		Explicit: explicitID != "",
		All:      r.Bool("--all"),
		Force:    r.Bool("--force"),
	}

	sweepPass(explicitID, apply, r.Bool("--verbose"), opts)
	if intervalSec == 0 {
		return
	}
	// Loop mode: the daemon cadence (run it alongside robots-watch).
	for {
		time.Sleep(time.Duration(intervalSec * float64(time.Second)))
		sweepPass(explicitID, apply, r.Bool("--verbose"), opts)
	}
}

// sweepPass runs one full pass: enumerate → classify → report → (optionally)
// tear down. A teardown refusal is reported and the pass continues; one
// stuck agent must never stall the collector.
func sweepPass(explicitID string, apply, verbose bool, opts SweepOpts) {
	opts.Keep = readSweepKeep()
	ids := []string{explicitID}
	if explicitID == "" {
		ids = sweepCandidates()
	}

	var swept, refused, held, skipped int
	for _, id := range ids {
		agent := resolveSweepAgent(id)
		if agent.NotFound {
			if explicitID != "" {
				httpc.Die(fmt.Sprintf("parlay sweep: agent %q not found in agents dir", id), config.ExitUsage)
				return
			}
			continue
		}
		v := ClassifySweep(agent, opts)
		switch v.Action {
		case SweepSkip:
			skipped++
			if verbose {
				fmt.Printf("skip     %s — %s\n", id, v.Reason)
			}
		case SweepHold:
			held++
			fmt.Printf("HOLD     %s — %s\n", id, v.Reason)
		case SweepTeardown:
			if !apply {
				swept++
				fmt.Printf("would-close %s — %s\n", id, v.Reason)
				continue
			}
			msg, err := teardownAgent(id, false)
			if err != nil {
				refused++
				fmt.Printf("REFUSED  %s — %v\n", id, err)
				continue
			}
			swept++
			fmt.Printf("closed   %s — %s\n", id, msg)
		}
	}

	verb := "would close"
	if apply {
		verb = "closed"
	}
	fmt.Printf("sweep: %d %s, %d refused, %d held, %d skipped (of %d)\n", swept, verb, refused, held, skipped, len(ids))
	if !apply && swept > 0 {
		fmt.Fprintln(os.Stderr, "dry run — re-run with --apply to actually close them")
	}
}
