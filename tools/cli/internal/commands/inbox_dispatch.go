// parlay inbox-dispatch — kill switch for the inbox→handler auto-dispatcher.
// Lets the captain pause and resume automatic inbox dispatch with one
// command, without touching launchd or killing the watcher daemon.
//
// The gate lives in internal/robotswatch.inboxDispatchOff() and is checked
// inside dispatchInbox() — the choke point the POLL path converges on. When
// OFF the poller keeps running and advancing its cursor normally; only the
// spawn is skipped, so re-enabling does NOT replay the backlog.
//
// Disabled state = presence of $PARLAY_STATE_HOME/inbox-dispatch.off (default
// ~/.parlay/inbox-dispatch.off), OR env PARLAY_INBOX_DISPATCH=off.
// PARLAY_INBOX_DISPATCH=on does NOT force-enable past a present sentinel —
// the sentinel is the durable operator intent and wins.
//
// Independent from `parlay mechanic`'s gate: pausing inbox dispatch leaves
// robots dispatch running and vice versa.
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

func inboxDispatchSentinelPath() string {
	return filepath.Join(config.StateHome(), "inbox-dispatch.off")
}

// InboxDispatch is `parlay inbox-dispatch`'s entry point.
func InboxDispatch(argv []string) {
	if helpWanted("inbox-dispatch", argv) {
		return
	}
	sub := ""
	if len(argv) > 0 {
		sub = argv[0]
	}
	switch sub {
	case "off":
		inboxDispatchOffCmd()
	case "on":
		inboxDispatchOnCmd()
	case "status":
		inboxDispatchStatus()
	default:
		httpc.Die("parlay inbox-dispatch: subcommand required: on | off | status", config.ExitUsage)
	}
}

func inboxDispatchOffCmd() {
	path := inboxDispatchSentinelPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		httpc.Die(fmt.Sprintf("parlay inbox-dispatch off: %s", err), config.ExitRuntime)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay inbox-dispatch off: %s", err), config.ExitRuntime)
		return
	}
	f.Close()
	fmt.Printf("inbox dispatch: OFF\nsentinel: %s\n", path)
}

func inboxDispatchOnCmd() {
	path := inboxDispatchSentinelPath()
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		httpc.Die(fmt.Sprintf("parlay inbox-dispatch on: %s", err), config.ExitRuntime)
		return
	}
	fmt.Printf("inbox dispatch: ON\nsentinel removed: %s\n", path)
}

// inboxDispatchStateInfo computes the current dispatch state from the sentinel
// file and env. Factored out so tests can exercise the logic without calling
// os.Exit.
type inboxDispatchState struct {
	Off    bool
	Reason string // non-empty when Off
	Path   string
}

func inboxDispatchStateInfo() inboxDispatchState {
	path := inboxDispatchSentinelPath()
	envVal := strings.ToLower(strings.TrimSpace(os.Getenv("PARLAY_INBOX_DISPATCH")))
	_, sentinelErr := os.Stat(path)
	sentinelPresent := sentinelErr == nil

	switch {
	case envVal == "off":
		reason := "PARLAY_INBOX_DISPATCH=off"
		if sentinelPresent {
			reason += " (sentinel also present)"
		}
		return inboxDispatchState{Off: true, Reason: reason, Path: path}
	case sentinelPresent:
		reason := "sentinel file present"
		if envVal == "on" {
			reason += " (PARLAY_INBOX_DISPATCH=on ignored — sentinel is operator intent)"
		}
		return inboxDispatchState{Off: true, Reason: reason, Path: path}
	default:
		return inboxDispatchState{Off: false, Path: path}
	}
}

func inboxDispatchStatus() {
	s := inboxDispatchStateInfo()
	if s.Off {
		fmt.Printf("inbox dispatch: off\nreason: %s\nsentinel: %s\n", s.Reason, s.Path)
	} else {
		fmt.Printf("inbox dispatch: on\nsentinel: %s (absent)\n", s.Path)
	}
}
