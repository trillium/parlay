// `parlay launch` — discover known agents and spawn one via the spawner
// binary (parlay-bin, falling back to parlay-spawn).
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

// spawnerNames lists the agent-spawning binaries in preference order:
// parlay-bin (the Go port, ticket A1, which takes a `spawn` subcommand) then
// parlay-spawn (the bash original this repo still ships in bin/, same
// positional contract with no subcommand word). config.SpawnImpl()
// (PARLAY_SPAWN_IMPL env var, else config.toml's `spawnImpl` key) overrides
// this order entirely — see resolveSpawnerChoice.
var spawnerNames = []string{"parlay-bin", "parlay-spawn"}
var spawnerSubcommand = map[string]string{"parlay-bin": "spawn"}

// spawnerChoice is which binary resolveSpawnerChoice picked, plus whether
// that pick was an explicit PARLAY_SPAWN_IMPL/config override (explicit) or
// the auto-preference order (!explicit). Spawn (spawn.go) uses explicit to
// decide whether a Go binary that fails to even start may fall back to bash,
// or must fail loudly instead — see execSpawner.
type spawnerChoice struct {
	bin      string
	argv     []string
	name     string
	explicit bool
}

// resolveSpawnerChoice implements the documented precedence
// (docs/scope-go-spawn.md Stage 4) for which binary executes a spawn:
//
//  1. config.SpawnImpl() ("go" or "bash", case-insensitive; PARLAY_SPAWN_IMPL
//     env var, else config.toml's top-level `spawnImpl` key). An explicit
//     choice is authoritative and never auto-falls-back: "go" fails loudly if
//     parlay-bin is not on PATH, and "bash" is the documented escape hatch
//     for a broken or untrusted Go binary. Any other non-empty value is a
//     usage error — a typo must not silently resolve to auto mode.
//  2. Otherwise, parlay-bin when it resolves on PATH (auto-preferred, marked
//     !explicit — a parlay-bin picked this way that fails to even start may
//     still fall back to bash at exec time; see spawn.go's execSpawner).
//  3. Otherwise, bin/parlay-spawn (bash).
//
// robots-v81b: resolution used to hardcode exec.Command("parlay-bin", …) and
// discard the run error, faithfully replicating launch.ts's unchecked
// Bun.spawnSync. The A1 rename was never accompanied by an install of
// parlay-bin anywhere, so on a host whose PATH carries only the
// bin/parlay-spawn symlink, every `parlay launch <id>` printed a spawning
// announcement, exited 0, and launched nothing — ENOENT was indistinguishable
// from success. Resolving both names fixes the common case; the error
// returned here is fatal at the call site so an unresolvable spawner is loud.
func resolveSpawnerChoice(spawnArgs []string) (spawnerChoice, error) {
	if override := strings.TrimSpace(config.SpawnImpl()); override != "" {
		switch strings.ToLower(override) {
		case "go":
			abs, lookErr := exec.LookPath("parlay-bin")
			if lookErr != nil || abs == "" {
				return spawnerChoice{}, fmt.Errorf("%s=go (or config.toml spawnImpl=go) demands parlay-bin, but it is not on PATH", config.SpawnImplEnv)
			}
			return spawnerChoice{bin: abs, argv: append([]string{"spawn"}, spawnArgs...), name: "parlay-bin", explicit: true}, nil
		case "bash":
			abs, lookErr := exec.LookPath("parlay-spawn")
			if lookErr != nil || abs == "" {
				return spawnerChoice{}, fmt.Errorf("%s=bash (or config.toml spawnImpl=bash) demands parlay-spawn, but it is not on PATH", config.SpawnImplEnv)
			}
			return spawnerChoice{bin: abs, argv: spawnArgs, name: "parlay-spawn", explicit: true}, nil
		default:
			return spawnerChoice{}, fmt.Errorf("invalid %s (or config.toml spawnImpl) value %q — must be \"go\" or \"bash\"", config.SpawnImplEnv, override)
		}
	}
	for _, name := range spawnerNames {
		abs, lookErr := exec.LookPath(name)
		if lookErr != nil || abs == "" {
			continue
		}
		if sub := spawnerSubcommand[name]; sub != "" {
			return spawnerChoice{bin: abs, argv: append([]string{sub}, spawnArgs...), name: name}, nil
		}
		return spawnerChoice{bin: abs, argv: spawnArgs, name: name}, nil
	}
	return spawnerChoice{}, fmt.Errorf("no spawner on PATH — install one of %s (this repo ships bin/parlay-spawn; symlink it into ~/.local/bin)", strings.Join(spawnerNames, " or "))
}

// resolveSpawner is resolveSpawnerChoice's (bin, argv, err) shape, kept for
// callers (Launch) that don't need to know whether the pick was explicit.
func resolveSpawner(spawnArgs []string) (bin string, argv []string, err error) {
	choice, err := resolveSpawnerChoice(spawnArgs)
	if err != nil {
		return "", nil, err
	}
	return choice.bin, choice.argv, nil
}

// spawnUsageHint describes how agents are created, naming whichever spawner
// this host actually has rather than a binary the reader may not own.
func spawnUsageHint() string {
	name := spawnerNames[len(spawnerNames)-1]
	for _, candidate := range spawnerNames {
		if abs, err := exec.LookPath(candidate); err == nil && abs != "" {
			name = candidate
			break
		}
	}
	if sub := spawnerSubcommand[name]; sub != "" {
		name += " " + sub
	}
	return name + " <id> <name> <color> <prompt> [--cwd PATH]"
}

// Launch ports cmdLaunch: with no positional arg, lists known agents
// cross-referenced against which are currently live; with an agent id arg,
// spawns that agent via the resolved spawner binary (external — exec'd, not
// reimplemented).
func Launch(argv []string) {
	if helpWanted("launch", argv) {
		return
	}
	// --force is deliberately absent from the usage text: `parlay launch` has a
	// TS twin whose usage the parity harness diffs (robots-xaxt), and the only
	// place this override is ever needed is the closed-bead refusal below,
	// which names it in full.
	r := args.Parse("launch", argv, []string{"--force"}, nil)
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
		bin, argv, err := resolveSpawner(spawnArgs)
		if err != nil {
			httpc.Die(fmt.Sprintf("parlay launch: cannot spawn %s — %v", target.id, err), config.ExitRuntime)
			return
		}
		spawner := filepath.Base(bin)
		fmt.Fprintf(os.Stderr, "parlay launch: spawning %s via %s …\n", target.id, spawner)
		// Blocking with inherited stdio, as launch.ts's Bun.spawnSync was —
		// but the result is checked. A spawner that cannot start, or that
		// exits non-zero, is a failed launch and must not report success.
		cmd := exec.Command(bin, argv...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			httpc.Die(fmt.Sprintf("parlay launch: %s failed to spawn %s — %v", spawner, target.id, err), config.ExitRuntime)
		}
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
