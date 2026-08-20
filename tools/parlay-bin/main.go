// Command parlay-bin is the Go port of bin/parlay-spawn + bin/context-reset
// (+ the bin/reincarnate alias). See docs/scope-go-spawn.md for the full
// scoping analysis this port follows.
package main

import (
	"fmt"
	"os"
)

const topUsage = `parlay-bin — Go port of bin/parlay-spawn + bin/context-reset

Usage:
  parlay-bin spawn ...        launch a new background claude agent (bin/parlay-spawn)
  parlay-bin reset ...        clean self-restart for a persistent agent (bin/context-reset)
  parlay-bin reincarnate ...  alias for 'reset' (legacy invocation name)
  parlay-bin gascity-spawn ...  start a detached headless session (herdr-free launcher path)
  parlay-bin gascity-stop ...   stop a gascity-spawn session
  parlay-bin gascity-ping ...   exit 0 if a gascity-spawn session is alive, 1 if not
`

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
	case "gascity-spawn":
		os.Exit(runGascitySpawnCommand(os.Args[2:]))
	case "gascity-stop":
		os.Exit(runGascityStopCommand(os.Args[2:]))
	case "gascity-ping":
		os.Exit(runGascityPingCommand(os.Args[2:]))
	case "-h", "--help":
		fmt.Fprint(os.Stderr, topUsage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "parlay-bin: unknown subcommand %q\n\n", os.Args[1])
		fmt.Fprint(os.Stderr, topUsage)
		os.Exit(2)
	}
}
