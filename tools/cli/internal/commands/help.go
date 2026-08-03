// Package commands holds the parlay CLI's simple read/write subcommands —
// one file per command group, mirroring packages/cli/src/commands.ts,
// commands-remote.ts, commands-nickname.ts, and commands-agent-down.ts
// (ticket B3, docs/scope-go-cli.md).
package commands

import (
	"fmt"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/help"
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
