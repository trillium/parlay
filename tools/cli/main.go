// Command parlay talks to a Parlay chat server.
//
// Go port of packages/cli (ticket B0, see docs/scope-go-cli.md and
// docs/plan-go-migration-tickets.md). Server URL from PARLAY_SERVER
// (default http://localhost:4242) — see internal/config.
//
// Exit codes: 0 = ok, 1 = runtime/server error, 2 = usage error (bad
// flag/command/args).
//
// This file is only the command dispatcher; each concern lives in its own
// package, mirroring index.ts's module split:
//
//	internal/config   server URL + exit codes    internal/args    flag parser
//	internal/httpc     JSON transport + die()      internal/format  message rendering
//	internal/wire      wire shapes                 internal/help    usage + per-command help
//
// B1 wired `identity`/`scratchpad` (internal/identity); B3 wired the simple
// read/write commands (subscribers, agents, agent-down, remote, nickname,
// send, alert, history, stats); B2 wired `monitor`/`listen`
// (internal/monitor, `agent-up` is an alias for `listen`). B5 wired
// `status` (the fold §3.6 keyed verb — bare `parlay` stays the panel/fleet
// snapshot), `crew-state`, `supervise`, and `context-check`; B4 wired
// `guard`/`teardown`/`variant` (internal/commands, the git-shell-out command
// chains); B7 wired `doctor`/`health` (internal/commands/doctor.go, ported
// from commands-doctor.ts). B8 wired `say`/`reply` (identity.CmdSay,
// already implemented in B1 alongside the resolve-handoff/say-guard
// death-window primitives it depends on — internal/resolvehandoff,
// internal/sayguard — but left undispatched here until this ticket); B6
// wired `robots-watch`/`robots-tail` (internal/robotswatch, the
// panic-isolated poll daemon + tailer); B9 wired `launch`/`drawdown`/`idle`
// (internal/commands/{launch,drawdown,idle}.go, ported from
// packages/cli/src/commands/{launch,drawdown,idle}.ts). Every other
// subcommand from the TS CLI's dispatch lands here as its own ticket adds it.
package main

import (
	"fmt"
	"os"

	"github.com/trillium/parlay/tools/cli/internal/commands"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/help"
	"github.com/trillium/parlay/tools/cli/internal/identity"
	"github.com/trillium/parlay/tools/cli/internal/monitor"
	"github.com/trillium/parlay/tools/cli/internal/robotswatch"
)

func main() {
	argv := os.Args[1:]
	var cmd string
	var args []string
	if len(argv) > 0 {
		cmd, args = argv[0], argv[1:]
	}

	switch cmd {
	case "":
		commands.Status() // bare `parlay` = panel/fleet snapshot
	case "status":
		commands.StatusVerb(args) // fold §3.6 keyed status verb — distinct from bare `parlay`
	case "crew-state":
		commands.CrewState(args)
	case "supervise":
		commands.Supervise(args)
	case "context-check":
		commands.ContextCheck(args)
	case "subscribers":
		commands.Subscribers(args)
	case "agents":
		commands.Agents(args)
	case "agent-down":
		commands.AgentDown(args)
	case "remote":
		commands.Remote(args)
	case "nickname":
		commands.Nickname(args)
	case "send":
		commands.Send(args)
	case "alert":
		commands.Alert(args)
	case "history":
		commands.History(args)
	case "stats":
		commands.Stats(args)
	case "help", "--help", "-h":
		cmdHelp(args)
	case "identity":
		identity.CmdIdentity(args)
	case "scratchpad":
		identity.CmdScratchpad(args)
	case "say", "reply":
		identity.CmdSay(args)
	case "monitor":
		monitor.CmdMonitor(args)
	case "listen", "agent-up":
		monitor.CmdListen(args)
	case "guard":
		commands.Guard(args)
	case "teardown":
		commands.Teardown(args)
	case "variant":
		commands.Variant(args)
	case "doctor":
		commands.Doctor(args)
	case "health":
		commands.Health(args)
	case "robots-watch":
		robotswatch.CmdRobotsWatch(args)
	case "robots-tail":
		robotswatch.CmdRobotsTail(args)
	case "launch":
		commands.Launch(args)
	case "drawdown":
		commands.Drawdown(args)
	case "idle":
		commands.Idle(args)
	default:
		fmt.Fprintf(os.Stderr, "parlay: unknown command or flag %q — run 'parlay help' for usage\n", cmd)
		os.Exit(config.ExitUsage)
	}
}

// cmdHelp prints the help text for a named subcommand ("parlay help <cmd>"),
// or the full USAGE when no subcommand is given or none matches — the same
// fallback as help.ts's helpWanted() using HELP[cmd] ?? USAGE.
func cmdHelp(args []string) {
	if len(args) > 0 {
		if text, ok := help.Lookup(args[0]); ok {
			fmt.Println(text)
			return
		}
	}
	fmt.Println(help.Usage(config.ServerURL()))
}
