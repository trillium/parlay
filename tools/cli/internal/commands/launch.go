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
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

type knownAgent struct {
	id, name, color, cwd, model string
}

// knownAgents discovers agents from ~/.parlay/agents/*/identity.md
// frontmatter (id/name/color/cwd/model), mirroring launch.ts's directory
// scan. Reuses parlayAgentsDir/readLocalFrontmatter from guard.go: launch.ts
// hardcodes join(homedir(), ".parlay", "agents") and defines its own local
// parseFrontmatter with the identical `/^---\n([\s\S]*?)\n---/` +
// `/^(\w+):\s*"?([^"]*)"?\s*$/` regex pair guard.go already carries for
// commands-teardown.ts/commands-variant.ts's local parseFm — same shape, so
// no third copy here.
func knownAgents() []knownAgent {
	var known []knownAgent
	entries, err := os.ReadDir(parlayAgentsDir())
	if err != nil {
		return known
	}
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		fm := readLocalFrontmatter(filepath.Join(parlayAgentsDir(), d.Name(), "identity.md"))
		id, name, color := fm.Get("id"), fm.Get("name"), fm.Get("color")
		if id == "" || name == "" || color == "" {
			continue
		}
		cwd := fm.Get("cwd")
		if cwd == "" {
			cwd = parlayHomeDir()
		}
		known = append(known, knownAgent{id: id, name: name, color: color, cwd: cwd, model: fm.Get("model")})
	}
	return known
}

// spawnerNames lists the agent-spawning binaries in preference order:
// parlay-bin (the Go port, ticket A1, which takes a `spawn` subcommand) then
// parlay-spawn (the bash original this repo still ships in bin/, same
// positional contract with no subcommand word).
var spawnerNames = []string{"parlay-bin", "parlay-spawn"}
var spawnerSubcommand = map[string]string{"parlay-bin": "spawn"}

// resolveSpawner returns the argv for launching an agent with the first
// spawner actually present on PATH, plus the basename to name in messages.
//
// robots-v81b: this used to hardcode exec.Command("parlay-bin", …) and
// discard the run error, faithfully replicating launch.ts's unchecked
// Bun.spawnSync. The A1 rename was never accompanied by an install of
// parlay-bin anywhere, so on a host whose PATH carries only the
// bin/parlay-spawn symlink, every `parlay launch <id>` printed a spawning
// announcement, exited 0, and launched nothing — ENOENT was indistinguishable
// from success. Resolving both names fixes the common case; the error
// returned here is fatal at the call site so an unresolvable spawner is loud.
func resolveSpawner(spawnArgs []string) (bin string, argv []string, err error) {
	for _, name := range spawnerNames {
		abs, lookErr := exec.LookPath(name)
		if lookErr != nil || abs == "" {
			continue
		}
		if sub := spawnerSubcommand[name]; sub != "" {
			return abs, append([]string{sub}, spawnArgs...), nil
		}
		return abs, spawnArgs, nil
	}
	return "", nil, fmt.Errorf("no spawner on PATH — install one of %s (this repo ships bin/parlay-spawn; symlink it into ~/.local/bin)", strings.Join(spawnerNames, " or "))
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
	r := args.Parse("launch", argv, nil, nil)
	known := knownAgents()

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
		revival := "Your context was reset. Follow the recovery chain above (identity → handoff → scratchpad) to restore your state, then await the captain."
		spawnArgs := []string{target.id, target.name, target.color, revival, "--cwd", target.cwd}
		if target.model != "" {
			spawnArgs = append(spawnArgs, "--model", target.model)
		}
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
	if len(known) == 0 {
		fmt.Printf("No agent homes found in %s\n", parlayAgentsDir())
		fmt.Println("Agents are created with: " + spawnUsageHint())
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
	for _, a := range known {
		status := "[offline]"
		if liveSet[a.id] {
			status = "[live]   "
		}
		fmt.Printf("  %s %s %s  %s %s\n", format.PadEnd(a.id, 16), format.PadEnd(a.name, 16), a.color, format.PadEnd(short(a.cwd), 32), status)
	}
	var offline []knownAgent
	for _, a := range known {
		if !liveSet[a.id] {
			offline = append(offline, a)
		}
	}
	if len(offline) > 0 {
		fmt.Fprintln(os.Stderr, "\nTo launch an offline agent:")
		for _, a := range offline {
			fmt.Fprintf(os.Stderr, "  parlay launch %s\n", a.id)
		}
	}
}
