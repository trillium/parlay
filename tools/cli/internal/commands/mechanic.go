// parlay mechanic — kill switch for the robots→mechanic auto-spawner.
// Lets the captain pause and resume automatic mechanic dispatch with one
// command, without touching launchd or killing the tailer/watcher daemons.
//
// The gate lives in internal/robotswatch.mechanicDispatchOff() and is checked
// inside dispatchMechanic() — the single choke point both the PUSH (robots-tail)
// and POLL (robots-watch) paths converge on. When OFF the tailer and poller
// continue running and advancing their offsets normally; only the spawn is
// skipped, so re-enabling does NOT replay the backlog.
//
// Disabled state = presence of $PARLAY_STATE_HOME/mechanic-dispatch.off (default
// ~/.parlay/mechanic-dispatch.off), OR env PARLAY_MECHANIC_DISPATCH=off.
// PARLAY_MECHANIC_DISPATCH=on does NOT force-enable past a present sentinel —
// the sentinel is the durable operator intent and wins.
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

func mechanicSentinelPath() string {
	return filepath.Join(config.StateHome(), "mechanic-dispatch.off")
}

// Mechanic is `parlay mechanic`'s entry point.
func Mechanic(argv []string) {
	if helpWanted("mechanic", argv) {
		return
	}
	sub := ""
	if len(argv) > 0 {
		sub = argv[0]
	}
	switch sub {
	case "off":
		mechanicOff()
	case "on":
		mechanicOn()
	case "status":
		mechanicStatus()
	default:
		httpc.Die("parlay mechanic: subcommand required: on | off | status", config.ExitUsage)
	}
}

func mechanicOff() {
	path := mechanicSentinelPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		httpc.Die(fmt.Sprintf("parlay mechanic off: %s", err), config.ExitRuntime)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay mechanic off: %s", err), config.ExitRuntime)
		return
	}
	f.Close()
	fmt.Printf("mechanic dispatch: OFF\nsentinel: %s\n", path)
}

func mechanicOn() {
	path := mechanicSentinelPath()
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		httpc.Die(fmt.Sprintf("parlay mechanic on: %s", err), config.ExitRuntime)
		return
	}
	fmt.Printf("mechanic dispatch: ON\nsentinel removed: %s\n", path)
}

// mechStateInfo computes the current dispatch state from the sentinel file and
// env. Factored out so tests can exercise the logic without calling os.Exit.
type mechState struct {
	Off    bool
	Reason string // non-empty when Off
	Path   string
}

func mechStateInfo() mechState {
	path := mechanicSentinelPath()
	envVal := strings.ToLower(strings.TrimSpace(os.Getenv("PARLAY_MECHANIC_DISPATCH")))
	_, sentinelErr := os.Stat(path)
	sentinelPresent := sentinelErr == nil

	switch {
	case envVal == "off":
		reason := "PARLAY_MECHANIC_DISPATCH=off"
		if sentinelPresent {
			reason += " (sentinel also present)"
		}
		return mechState{Off: true, Reason: reason, Path: path}
	case sentinelPresent:
		reason := "sentinel file present"
		if envVal == "on" {
			reason += " (PARLAY_MECHANIC_DISPATCH=on ignored — sentinel is operator intent)"
		}
		return mechState{Off: true, Reason: reason, Path: path}
	default:
		return mechState{Off: false, Path: path}
	}
}

func mechanicStatus() {
	s := mechStateInfo()
	if s.Off {
		fmt.Printf("mechanic dispatch: off\nreason: %s\nsentinel: %s\n", s.Reason, s.Path)
	} else {
		fmt.Printf("mechanic dispatch: on\nsentinel: %s (absent)\n", s.Path)
	}
}
