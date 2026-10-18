// `parlay launch` — discover known agents and respawn one via the same
// in-process spawn pipeline `parlay spawn` runs (see spawn.go's
// runSpawnArgv).
//
// Ported from packages/cli/src/commands/launch.ts (ticket B9).
package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/identity"
	"github.com/trillium/parlay/tools/cli/internal/monitor"
	"github.com/trillium/parlay/tools/cli/internal/spawn"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

type knownAgent struct {
	id, name, color, cwd, model string
	// account is the ccjuggler account name from the identity's `account:`
	// frontmatter key — which ccjuggler OAuth token the respawned agent runs
	// under. Empty means the identity does not pin one, and the config-level
	// default (config.SpawnAccount) applies instead.
	account string
	// identityFile is the path knownAgents() parsed this agent out of. The
	// closed-bead respawn gate needs it: identity.BoundWorkItemClosed resolves
	// the binding (spawn-time `bead:`, else claim-time `task:`) from the file
	// itself, so carrying the path — rather than a second copy of the id
	// parsed here — keeps ONE implementation of "which item governs this
	// agent" across `identity --launch`, `identity --submit` and this verb.
	identityFile string
}

// knownAgents discovers agents from ~/.parlay/agents/*/identity.md
// frontmatter (id/name/color/cwd/model/account), mirroring launch.ts's directory
// scan. Reuses parlayAgentsDir/readLocalFrontmatter from guard.go: launch.ts
// hardcodes join(homedir(), ".parlay", "agents") and defines its own local
// parseFrontmatter with the identical `/^---\n([\s\S]*?)\n---/` +
// `/^(\w+):\s*"?([^"]*)"?\s*$/` regex pair guard.go already carries for
// commands-teardown.ts/commands-variant.ts's local parseFm — same shape, so
// no third copy here.
// The second return counts agent homes that exist on disk but were skipped
// because their identity.md carries no complete id/name/color launch spec —
// a home created by a bare `identity '<fact>'` write, not `identity
// --register`. The no-agents listing needs the distinction: reporting "no
// agent homes found" over a directory that visibly contains homes reads as
// a lie and hides the actual repair (seed the frontmatter).
func knownAgents() ([]knownAgent, int) {
	var known []knownAgent
	unlaunchable := 0
	entries, err := os.ReadDir(parlayAgentsDir())
	if err != nil {
		return known, unlaunchable
	}
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		identityFile := filepath.Join(parlayAgentsDir(), d.Name(), "identity.md")
		fm := readLocalFrontmatter(identityFile)
		id, name, color := fm.Get("id"), fm.Get("name"), fm.Get("color")
		if id == "" || name == "" || color == "" {
			unlaunchable++
			continue
		}
		cwd := fm.Get("cwd")
		if cwd == "" {
			cwd = parlayHomeDir()
		}
		known = append(known, knownAgent{id: id, name: name, color: color, cwd: cwd, model: fm.Get("model"), account: fm.Get("account"), identityFile: identityFile})
	}
	return known, unlaunchable
}

// spawnUsageHint describes how agents are created. Since task-42qot the
// spawn pipeline runs in-process — `parlay spawn` is the single entry
// point, so the hint no longer probes PATH for spawner binaries.
func spawnUsageHint() string {
	return "parlay spawn <id> <name> <color> <prompt> [--cwd PATH]"
}

// Launch ports cmdLaunch: with no positional arg, lists known agents
// cross-referenced against which are currently live; with an agent id arg,
// spawns that agent through the in-process spawn pipeline.
func Launch(argv []string) {
	if helpWanted("launch", argv) {
		return
	}
	// --bead threads a beads work item through to the spawn pipeline;
	// --force bypasses both the closed-bead refusal below and the pipeline's
	// beads-required gate (forwarded, not just consulted here — robots-94ce
	// found launch parsing --force but never forwarding it, so a forced
	// relaunch still died in beadGate).
	r := args.Parse("launch", argv, []string{"--force"}, []string{"--bead"})
	known, unlaunchable := knownAgents()

	if len(r.Positionals) > 0 {
		targetID := r.Positionals[0]
		var target *knownAgent
		for i := range known {
			if known[i].id == targetID {
				target = &known[i]
				break
			}
		}
		if target == nil {
			httpc.Die(fmt.Sprintf("parlay launch: no known agent '%s' — run 'parlay launch' to list available agents", targetID), config.ExitUsage)
			return
		}
		// Closed-bead respawn gate (beads-required mode). The bead bound at
		// spawn time governs the agent's lifecycle: once it is closed the work
		// is over, and re-spawning burns a whole fresh context on an agent that
		// can only rediscover there is nothing to do and shut down again —
		// exactly the loop `identity --launch` already refuses (lifecycle.go's
		// guard). This is the same oracle, so both entrances to a relaunch
		// agree, and it inherits the same FAIL-OPEN contract: only an
		// affirmative closed status refuses, so a missing binding or a store
		// hiccup still launches.
		//
		// A refusal is a successful no-op, not an error (exit 0), matching
		// HandleLaunch: "that work is finished" is the answer the caller asked
		// for, and a non-zero exit would make every supervisor treat a clean
		// end as a failed spawn.
		if !r.Bool("--force") {
			if item, closed := boundWorkItemClosed(target.identityFile); closed {
				fmt.Printf("parlay launch %s: bound work item %s is CLOSED — NOT spawning (clean end).\n", target.id, item)
				fmt.Fprintf(os.Stderr, "  the agent's work is done; re-open %s, or run 'parlay launch %s --force' to spawn anyway.\n", item, target.id)
				return
			}
		}
		revival := "Your context was reset. Follow the recovery chain above (identity → handoff → scratchpad) to restore your state, then await the captain."
		spawnArgs := []string{target.id, target.name, target.color, revival, "--cwd", target.cwd}
		if target.model != "" {
			spawnArgs = append(spawnArgs, "--model", target.model)
		}
		spawnArgs = append(spawnArgs, identity.SpawnAccountArgs(target.account)...)
		// Thread the bead through the launch path (robots-94ce): the spawn
		// pipeline refuses without one when beads-required mode is on, and
		// `parlay launch` never supplied it — so every ghost/offline
		// relaunch died with exit 2. Precedence: explicit --bead wins;
		// otherwise a still-OPEN bound item is reused (no duplicate
		// tracking bead); otherwise a fresh tracking bead is minted in the
		// default (task) store. --force skips all of this and is forwarded
		// so the pipeline's own gate stands down too.
		if bead, ok := r.String("--bead"); ok && strings.TrimSpace(bead) != "" {
			spawnArgs = append(spawnArgs, "--bead", strings.TrimSpace(bead))
		} else if r.Bool("--force") {
			spawnArgs = append(spawnArgs, "--force")
		} else if launchBeadsRequired() {
			if item, closed := boundWorkItemClosed(target.identityFile); item != "" && !closed {
				fmt.Fprintf(os.Stderr, "parlay launch: reusing open bound work item %s for %s …\n", item, target.id)
				spawnArgs = append(spawnArgs, "--bead", item)
			} else if newID, err := launchCreateBead(target.id, target.name); err != nil {
				fmt.Fprintf(os.Stderr, "parlay launch: WARNING — could not mint a tracking bead for %s: %v.\n", target.id, err)
				fmt.Fprintln(os.Stderr, "  Proceeding without one; the spawn may refuse — pass --bead <id> explicitly, or --force to bypass.")
			} else {
				fmt.Fprintf(os.Stderr, "parlay launch: minted tracking bead %s for %s …\n", newID, target.id)
				spawnArgs = append(spawnArgs, "--bead", newID)
			}
		}
		fmt.Fprintf(os.Stderr, "parlay launch: spawning %s via parlay spawn …\n", target.id)
		// Same dispatch as `parlay spawn` itself (spawn.go), in-process.
		// A failed spawn propagates its real exit code via
		// httpc.Exit rather than reporting success (robots-v81b's lesson:
		// a swallowed spawn failure is indistinguishable from a launch).
		runSpawnArgv(spawnArgs)
		return
	}

	// Cross-reference against the live agent registry. A down/unreachable
	// server degrades to "no agents live" (TryGetJSON), matching launch.ts's
	// try/catch around this getJSON call — unlike drawdown's history fetch,
	// this one must not die the process.
	var live []wire.AgentInfo
	if l, ok := httpc.TryGetJSON[[]wire.AgentInfo]("/api/chat/agents", 3*time.Second); ok {
		live = l
	}
	liveSet := make(map[string]bool, len(live))
	for _, a := range live {
		liveSet[a.ID] = true
	}
	// Registry membership alone is a display LIE (robots-jkwc): a registration
	// is a row on the server that nothing ever removes when a listener dies,
	// so `parlay launch` reported 148 agents [live] against 11 real listener
	// processes. Intersect with the process table for the truthful answer.
	listeners, listenersKnown := liveListeners()
	if len(known) == 0 {
		if unlaunchable > 0 {
			fmt.Printf("No launchable agents in %s — %d agent home(s) exist but lack the id/name/color identity frontmatter a launch needs\n", parlayAgentsDir(), unlaunchable)
			fmt.Println("Seed one with: parlay identity --register --name <name> --color <hex> (as that agent), or create agents with: " + spawnUsageHint())
		} else {
			fmt.Printf("No agent homes found in %s\n", parlayAgentsDir())
			fmt.Println("Agents are created with: " + spawnUsageHint())
		}
		return
	}
	home := parlayHomeDir()
	short := func(p string) string {
		if strings.HasPrefix(p, home) {
			return "~" + p[len(home):]
		}
		return p
	}
	fmt.Printf("%d known agent(s):\n", len(known))
	var offline, ghosts []knownAgent
	for _, a := range known {
		status := launchStatus(liveSet[a.id], listeners[a.id], listenersKnown)
		switch status {
		case statusOffline:
			offline = append(offline, a)
		case statusGhost:
			ghosts = append(ghosts, a)
		}
		fmt.Printf("  %s %s %s  %s %s\n", format.PadEnd(a.id, 16), format.PadEnd(a.name, 16), a.color, format.PadEnd(short(a.cwd), 32), status)
	}
	if len(ghosts) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d agent(s) are registered with NO listener process on this host —\n", len(ghosts))
		fmt.Fprintln(os.Stderr, "  nothing reads their channel, so a message sent to one is silently lost.")
		fmt.Fprintln(os.Stderr, "  Re-arm the agent, or clear the dead registration:")
		for _, a := range ghosts {
			fmt.Fprintf(os.Stderr, "  parlay agent-down %s\n", a.id)
		}
	}
	if len(offline) > 0 {
		fmt.Fprintln(os.Stderr, "\nTo launch an offline agent:")
		for _, a := range offline {
			fmt.Fprintf(os.Stderr, "  parlay launch %s\n", a.id)
		}
	}
}

// liveListeners is the process-table probe, injectable so the tests can drive
// the classification without a real `ps` (a unit test cannot arm a listener).
var liveListeners = monitor.LiveListenerAgents

// boundWorkItemClosed is the closed-bead oracle, injectable for the same reason
// liveListeners is: the real one shells out to a federation store CLI, which a
// unit test has no business requiring on the box.
var boundWorkItemClosed = identity.BoundWorkItemClosed

// launchBeadsRequired reports whether the spawn pipeline will demand a bead.
// Injectable so tests can drive the mint/reuse branches without a real
// config.toml on the box.
var launchBeadsRequired = spawn.BeadsRequired

// launchCreateBead mints the relaunch tracking bead. Injectable so tests can
// observe the mint without a real beads store on the box.
var launchCreateBead = createLaunchTrackingBead

// createLaunchTrackingBead mints a tracking bead for a `parlay launch`
// relaunch in the default (task) store, returning the new bead id (e.g.
// "task-xxxx"). The bead's lifecycle governs the relaunched agent — the same
// contract a hand-passed --bead carries — so the documented ghost-recovery
// path (`parlay launch <id>`) works under beads-required mode with no extra
// flags (robots-94ce fix direction (a)).
//
// A creation failure is returned, never fatal here: the caller warns and
// dispatches without a bead, letting the spawn pipeline's own beadGate emit
// the authoritative refusal (with its --bead/--force guidance) rather than
// this verb inventing a second, divergent one.
func createLaunchTrackingBead(agentID, name string) (string, error) {
	bin, err := exec.LookPath("task")
	if err != nil {
		return "", fmt.Errorf("no 'task' store CLI on PATH: %w", err)
	}
	title := fmt.Sprintf("Relaunch agent %s (%s) — recovery", agentID, name)
	desc := fmt.Sprintf("Tracking bead auto-created by `parlay launch %s` (robots-94ce): binds the relaunched agent's lifecycle under beads-required mode.", agentID)
	cmd := exec.Command(bin, "create", title, "--description", desc, "--silent")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("task create failed: %w", err)
	}
	id := strings.TrimSpace(string(out))
	// --silent prints just the id; guard against a wrapper that chats anyway.
	if fields := strings.Fields(id); len(fields) == 1 && strings.Contains(fields[0], "-") {
		return fields[0], nil
	}
	return "", fmt.Errorf("task create returned an unparsable id: %q", string(out))
}

// The three states `parlay launch` can report, padded to one column width.
const (
	statusLive    = "[live]   "
	statusGhost   = "[ghost]  "
	statusOffline = "[offline]"
)

// launchStatus is the whole liveness classification, pure so the truthful
// reporting is testable without a server or a process table.
//
//	registered + a listener here    -> live
//	registered + NO listener here   -> ghost   (the registry's stale row)
//	not registered                  -> offline
//
// listenersKnown false means `ps` could not be read, so "no listener" carries
// no information: every registered agent stays [live] rather than being
// libelled as a ghost. Same asymmetry the singleton guard already runs on —
// a wrong [ghost] sends the captain to `agent-down` on a working agent, which
// costs more than a missed stale row.
func launchStatus(registered, hasListener, listenersKnown bool) string {
	switch {
	case !registered:
		return statusOffline
	case !listenersKnown || hasListener:
		return statusLive
	default:
		return statusGhost
	}
}
