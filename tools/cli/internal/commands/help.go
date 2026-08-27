// Package commands holds the parlay CLI's simple read/write subcommands —
// one file per command group, mirroring packages/cli/src/commands.ts,
// commands-remote.ts, commands-nickname.ts, and commands-agent-down.ts
// (ticket B3, docs/scope-go-cli.md).
package commands

import (
	"fmt"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/help"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// helpWanted mirrors help.ts's helpWanted(): if argv contains --help or -h
// anywhere, print that command's help text (falling back to full USAGE) and
// report true so the caller returns without doing any work.
func helpWanted(cmd string, argv []string) bool {
	wants := false
	for _, a := range argv {
		if a == "--help" || a == "-h" {
			wants = true
			break
		}
	}
	if !wants {
		return false
	}
	if text, ok := help.Lookup(cmd); ok {
		fmt.Println(text)
	} else {
		fmt.Println(help.Usage(config.ServerURL()))
	}
	return true
}

// rejectExtraArgs is the guard for a verb that takes no flags and no
// positionals. Call it directly after helpWanted, which must run first so
// `--help` still prints help rather than being rejected as an unexpected
// argument.
//
// It exists because silently ignoring leftover argv is the shape that produced
// the PR #115 bug: `parlay lavish-import --dry-run` performed a REAL import
// into the live Parlay at :31337, because the flag was accepted, dropped, and
// never looked at. A guessed safety flag did the opposite of safety.
//
// AGENTS.md states the rule this enforces: a dropped flag is not a degraded
// flag, it is a hard exit, because the caller may be discarding it. The caller
// cannot tell the difference between "this verb honoured my flag" and "this
// verb has no idea what I asked for" unless the verb says so.
//
// Returns true when it has exited, so callers read as `if rejectExtraArgs(…)
// { return }` — httpc.Die is injectable in tests and does not actually
// terminate there.
func rejectExtraArgs(cmd string, argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	httpc.Die(
		fmt.Sprintf("parlay %s: unexpected argument %q — this verb takes no flags or arguments", cmd, argv[0]),
		config.ExitUsage,
	)
	return true
}
