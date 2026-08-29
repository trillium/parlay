// Command parlay-bin is the Go port of bin/parlay-spawn + bin/context-reset
// (+ the bin/reincarnate alias). See docs/scope-go-spawn.md for the full
// scoping analysis this port follows.
package main

import (
	"fmt"
	"os"
	"strings"
)

const topUsage = `parlay-bin — Go port of bin/parlay-spawn + bin/context-reset

Usage:
  parlay-bin spawn ...        launch a new background claude agent (bin/parlay-spawn)
  parlay-bin reset ...        clean self-restart for a persistent agent (bin/context-reset)
                              note: unlike bin/context-reset, this port does not echo
                              the pinned handoff to the pane on a clean end
  parlay-bin reincarnate ...  alias for 'reset' (legacy invocation name)
  parlay-bin subprocess-spawn ...  start a detached subprocess session (herdr-free launcher path)
  parlay-bin subprocess-stop ...   stop a subprocess-spawn session
  parlay-bin subprocess-ping ...   exit 0 if a subprocess-spawn session is alive, 1 if not
  parlay-bin gascity-spawn ...     deprecated aliases for subprocess-spawn/-stop/-ping
  parlay-bin gascity-stop ...      (still accepted; a deprecation notice is printed)
  parlay-bin gascity-ping ...
`

// deprecatedAliases maps the pre-rename launcher verbs to their subcommand
// handlers. They still work identically for one release, printing a notice.
var deprecatedAliases = map[string]func([]string) int{
	"gascity-spawn": runSubprocessSpawnCommand,
	"gascity-stop":  runSubprocessStopCommand,
	"gascity-ping":  runSubprocessPingCommand,
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, topUsage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "spawn":
		os.Exit(runSpawnCommand(os.Args[2:]))
	case "reset", "reincarnate":
		os.Exit(runResetCommand(os.Args[2:]))
	case "subprocess-spawn":
		os.Exit(runSubprocessSpawnCommand(os.Args[2:]))
	case "subprocess-stop":
		os.Exit(runSubprocessStopCommand(os.Args[2:]))
	case "subprocess-ping":
		os.Exit(runSubprocessPingCommand(os.Args[2:]))
	case "gascity-spawn", "gascity-stop", "gascity-ping":
		fmt.Fprintf(os.Stderr, "parlay-bin: WARNING — %q is deprecated; use %q. Still works; will be removed after the next release.\n", os.Args[1], "subprocess-"+strings.TrimPrefix(os.Args[1], "gascity-"))
		os.Exit(deprecatedAliases[os.Args[1]](os.Args[2:]))
	case "-h", "--help":
		fmt.Fprint(os.Stderr, topUsage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "parlay-bin: unknown subcommand %q\n\n", os.Args[1])
		fmt.Fprint(os.Stderr, topUsage)
		os.Exit(2)
	}
}
