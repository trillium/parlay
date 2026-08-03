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
// read/write commands (status, subscribers, agents, agent-down, remote,
// nickname, send, alert, history, stats); B2 wired `monitor`/`listen`
// (internal/monitor, `agent-up` is an alias for `listen`). `say`/`reply`
// are implemented in internal/identity too (identity.CmdSay) but not yet
// wired here — see that file's header comment. Every other subcommand from
// the TS CLI's dispatch (guard, ...) lands here as its own ticket adds it.
package main

import (
	"fmt"
	"os"

	"github.com/trillium/parlay/tools/cli/internal/commands"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/help"
	"github.com/trillium/parlay/tools/cli/internal/identity"
	"github.com/trillium/parlay/tools/cli/internal/monitor"
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
	case "monitor":
		monitor.CmdMonitor(args)
	case "listen", "agent-up":
		monitor.CmdListen(args)
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
