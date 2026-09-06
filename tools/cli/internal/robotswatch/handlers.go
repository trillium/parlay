// The watch table (SUBSCRIBE), store polling, the router (ROUTE), and the
// two shipped handlers (DELIVER). decision-4zr: parlay owns
// subscribe+route+deliver. Ported from handlers.ts.
package robotswatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// watch names which store, which transitions we care about. Adding a
// consumer is a new row here, not new machinery — the closed handler
// registry of decision-4zr.
type watch struct {
	Store string
	Kinds []EventKind
}

var watches = []watch{
	// robots: created → mechanic-dispatch (handler a); closed → notify the
	// originating agent stamped on the bead as notify:<channel> (robots-3q7n).
	{Store: "robots", Kinds: []EventKind{EventCreated, EventClosed}},
	{Store: "questions", Kinds: []EventKind{EventClosed}}, // → notify-requester (handler b)
	{Store: "task", Kinds: []EventKind{EventClosed}},      // → notify-requester (handler b)
}

// listStore runs `<store> list --all --json --limit 0`. ok is false (skip
// this store this pass) if the store CLI is missing, errors, or its output
// is unparseable — this never panics, so one bad store can't stall the
// others or kill the daemon.
func listStore(store string, verbose bool) (beads []Bead, ok bool) {
	cmd := exec.Command(store, "list", "--all", "--json", "--limit", "0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	if runErr != nil || stdout.Len() == 0 {
		if verbose {
			fmt.Fprintf(os.Stderr, "robots-watch: skip store '%s' (%s)\n", store, spawnFailureReason(runErr))
		}
		return nil, false
	}

	if err := json.Unmarshal(stdout.Bytes(), &beads); err != nil {
		// Could be valid JSON that just isn't an array shape (e.g. an
		// object) — TS treats that as "not an array" → empty result, not a
		// parse failure. Only a genuinely unparseable body counts as skip.
		var probe any
		if json.Unmarshal(stdout.Bytes(), &probe) != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "robots-watch: skip store '%s' (unparseable --json)\n", store)
			}
			return nil, false
		}
		return []Bead{}, true
	}
	return beads, true
}

// spawnFailureReason mirrors TS's `r.error?.message ?? \`exit ${r.status}\“.
func spawnFailureReason(runErr error) string {
	if runErr == nil {
		return "exit 0" // reached only when stdout was empty on a clean exit
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return fmt.Sprintf("exit %d", exitErr.ExitCode())
	}
	return runErr.Error()
}

// routeEvent maps `<store>:<kind>` → handler.
func routeEvent(ev RouteEvent, bead Bead, verbose bool) {
	key := fmt.Sprintf("%s:%s", ev.Store, ev.Kind)
	switch key {
	case "robots:created":
		handleRobotsCreated(ev, verbose) // handler (a)
	case "robots:closed", "questions:closed", "task:closed":
		handleRequestClosed(ev, bead, verbose) // handler (b)
	default:
		if verbose {
			fmt.Fprintf(os.Stderr, "robots-watch: no route for %s (%s)\n", key, ev.ID)
		}
	}
}

// ── Handler (a): robots bead CREATED → mechanic-dispatch <id> ───────────────

func handleRobotsCreated(ev RouteEvent, verbose bool) {
	dispatchMechanic(ev.ID, verbose)
}

// mechanicDispatchSentinelPath is the kill-switch sentinel file: when it
// exists, both trigger paths (POLL and TAILER) skip the spawn. Create with
// `parlay mechanic off`, remove with `parlay mechanic on`.
func mechanicDispatchSentinelPath() string {
	return filepath.Join(config.StateHome(), "mechanic-dispatch.off")
}

// mechanicDispatchOff returns true when dispatch is disabled.
// Precedence: PARLAY_MECHANIC_DISPATCH=off forces off (no sentinel needed);
// a sentinel file disables even when PARLAY_MECHANIC_DISPATCH=on (the env
// "on" value does NOT override an operator-set sentinel).
func mechanicDispatchOff() bool {
	if strings.ToLower(strings.TrimSpace(os.Getenv("PARLAY_MECHANIC_DISPATCH"))) == "off" {
		return true
	}
	_, err := os.Stat(mechanicDispatchSentinelPath())
	return err == nil
}

// dispatchMechanic is the reusable dispatch: spawn `mechanic-dispatch <id>`
// (idempotent — checks the zone's mechanic liveness and launches via
// `parlay spawn` only if down). Shared by the POLL path (handler a) and the
// TAILER fast path (robots-tail), so both triggers converge on one
// dispatch. Failure-isolated: never panics.
func dispatchMechanic(id string, verbose bool) {
	if mechanicDispatchOff() {
		fmt.Fprintf(os.Stderr, "robots-watch: mechanic dispatch is OFF, skipping %s\n", id)
		return
	}
	cmd := exec.Command("mechanic-dispatch", id)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		fmt.Fprintf(os.Stderr, "robots-watch: mechanic-dispatch not runnable for %s: %s\n", id, runErr.Error())
		return
	}

	out := strings.TrimSpace(stdout.String() + stderr.String())
	if runErr == nil {
		suffix := ""
		if verbose && out != "" {
			suffix = " — " + out
		}
		fmt.Fprintf(os.Stderr, "robots-watch: dispatched mechanic for %s%s\n", id, suffix)
	} else {
		fmt.Fprintf(os.Stderr, "robots-watch: mechanic-dispatch %s exited %d: %s\n", id, exitErr.ExitCode(), out)
	}
}

// ── Handler (b): request/question/task CLOSED → notify requester ───────────
// DELIVER: `parlay send --<channel> "<text>"` for each subscribed channel. A
// monitor on that channel (e.g. firstmate on `mayor`) wakes and reads it. We
// shell out to the `parlay` wrapper rather than post in-process on purpose:
// (1) the wrapper resolves PARLAY_SERVER exactly as every other caller does,
// and (2) it is a SEPARATE process, so a server-unreachable failure is a
// captured non-zero exit — it can never abort the daemon.
func handleRequestClosed(ev RouteEvent, bead Bead, verbose bool) {
	channels := notifyChannels(bead.Labels)
	if len(channels) == 0 {
		if verbose {
			fmt.Fprintf(os.Stderr, "robots-watch: %s closed but no notify:<channel> label — no subscriber\n", ev.ID)
		}
		return
	}
	title := strings.TrimSpace(bead.Title)
	text := fmt.Sprintf("✅ %s closed", ev.ID)
	if title != "" {
		text = fmt.Sprintf("✅ %s closed — %s", ev.ID, title)
	}
	for _, channel := range channels {
		cmd := exec.Command("parlay", "send", "--"+channel, text)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		runErr := cmd.Run()

		var exitErr *exec.ExitError
		switch {
		case runErr != nil && !errors.As(runErr, &exitErr):
			fmt.Fprintf(os.Stderr, "robots-watch: notify '%s' of %s — parlay not runnable: %s\n", channel, ev.ID, runErr.Error())
		case runErr != nil:
			out := strings.TrimSpace(stdout.String() + stderr.String())
			fmt.Fprintf(os.Stderr, "robots-watch: notify '%s' of %s exited %d: %s\n", channel, ev.ID, exitErr.ExitCode(), out)
		default:
			fmt.Fprintf(os.Stderr, "robots-watch: notified '%s' — %s closed\n", channel, ev.ID)
		}
	}
}
