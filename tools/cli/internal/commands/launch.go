// `parlay launch` — discover known agents and spawn one via parlay-bin.
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

// Launch ports cmdLaunch: with no positional arg, lists known agents
// cross-referenced against which are currently live; with an agent id arg,
// spawns that agent via `parlay-bin spawn` (an external binary — exec'd,
// not reimplemented, matching Bun.spawnSync).
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
		spawnArgs := []string{"spawn", target.id, target.name, target.color, revival, "--cwd", target.cwd}
		if target.model != "" {
			spawnArgs = append(spawnArgs, "--model", target.model)
		}
		fmt.Fprintf(os.Stderr, "parlay launch: spawning %s via parlay-bin spawn …\n", target.id)
		// Bun.spawnSync(["parlay-bin", "spawn", ...spawnArgs], { stdio:
		// ["inherit","inherit","inherit"] }) with its result never checked —
		// faithfully replicated: blocking, inherited stdio, exit code
		// discarded.
		cmd := exec.Command("parlay-bin", spawnArgs...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		_ = cmd.Run()
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
		fmt.Println("Agents are created with: parlay-bin spawn <id> <name> <color> <prompt> [--cwd PATH]")
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
