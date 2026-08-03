// parlay robots-watch — the MVP event poll-daemon (decision-4zr interim
// bridge). Ported from index.ts.
//
// The durable design (docs/CLI_VERBS_AND_EVENTS.md §2.4) is: beads owns EMIT
// (an app-blind on-status-change hook), parlay owns SUBSCRIBE+ROUTE+DELIVER.
// Until the beads EMIT hook exists, parlay STANDS IN for the missing emit
// with this poll loop: it polls each watched store's `<store> list --all
// --json`, diffs a persisted per-bead status cursor, and routes each
// detected (store, status-change) through a handler table. When the real
// EMIT lands, only the source swaps — the router + handlers are unchanged.
//
// Panic isolation: TS's runPollOnce wraps pollOnce in try/catch so a bad
// pass logs and continues instead of killing the launchd daemon, and
// pollOnce itself wraps each routed handler in its own try/catch so one
// failing handler can't lose the rest of the diff. The Go equivalent of a
// thrown exception bubbling to an enclosing try/catch is a panic bubbling to
// an enclosing recover — runPollOnce and handleRoutedEvent below are that
// boundary, at the same two levels as the TS source.
//
// Usage: parlay robots-watch [--interval <sec>] [--once] [--verbose]
// State: $PARLAY_STATE_HOME/robots-watch/cursor.json (default ~/.parlay/…).
// First sighting of a store SEEDS its cursor and fires nothing.
package robotswatch

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/help"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

const defaultIntervalSec = 15.0

// CmdRobotsWatch is `parlay robots-watch`'s entry point.
func CmdRobotsWatch(argv []string) {
	if help.Wanted("robots-watch", argv) {
		return
	}
	r := args.Parse("robots-watch", argv, []string{"--once", "--verbose"}, []string{"--interval"})

	once := r.Bool("--once")
	verbose := r.Bool("--verbose")
	intervalSec := defaultIntervalSec
	if raw, present := r.String("--interval"); present {
		raw = strings.TrimSpace(raw)
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
			httpc.Die(fmt.Sprintf("parlay robots-watch: --interval must be a positive number of seconds (got '%s')", raw), config.ExitUsage)
			return
		}
		intervalSec = n
	}

	mode := fmt.Sprintf("polling every %ss", formatSeconds(intervalSec))
	if once {
		mode = "single pass"
	}
	fmt.Fprintf(os.Stderr, "parlay robots-watch — %s (handlers: robots-created→mechanic-dispatch, request-closed→notify)\n", mode)

	runPollOnce(verbose)
	if once {
		return
	}

	// Continuous loop for the launchd daemon.
	for {
		time.Sleep(time.Duration(intervalSec * float64(time.Second)))
		runPollOnce(verbose)
	}
}

// formatSeconds renders intervalSec the way a JS template literal would
// stringify a number: no trailing ".0" for whole values, fractional values
// kept — same convention as commands.formatGrace / context_check.formatPercent.
func formatSeconds(n float64) string {
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// runPollOnce: a single bad pass must never kill the daemon — log and continue.
func runPollOnce(verbose bool) {
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Fprintf(os.Stderr, "robots-watch: poll pass failed (continuing): %v\n", rec)
		}
	}()
	pollOnce(verbose)
}

// pollOnce runs one poll pass: poll → diff → route → persist.
func pollOnce(verbose bool) {
	cursor := readCursor()
	for _, w := range watches {
		beads, ok := listStore(w.Store, verbose)
		if !ok {
			continue // store unavailable this pass; keep prior cursor
		}
		beadsByID := make(map[string]Bead, len(beads))
		curr := StoreState{}
		for _, b := range beads {
			beadsByID[b.ID] = b
			status := b.Status
			if status == "" {
				status = "open"
			}
			curr[b.ID] = status
		}

		events, seeded := detectEvents(cursor[w.Store], curr, w.Store, w.Kinds)
		if verbose {
			if seeded {
				fmt.Fprintf(os.Stderr, "robots-watch: %s — %d beads, SEEDED (no fire)\n", w.Store, len(beads))
			} else {
				fmt.Fprintf(os.Stderr, "robots-watch: %s — %d beads, %d event(s)\n", w.Store, len(beads), len(events))
			}
		}
		for _, ev := range events {
			handleRoutedEvent(ev, beadsByID[ev.ID], verbose)
		}
		cursor[w.Store] = curr // adopt current state (seed or advance)
	}
	writeCursor(cursor)
}

// handleRoutedEvent: a failing handler must not abort the pass or lose the
// rest of the diff.
func handleRoutedEvent(ev RouteEvent, bead Bead, verbose bool) {
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Fprintf(os.Stderr, "robots-watch: handler for %s:%s %s failed: %v\n", ev.Store, ev.Kind, ev.ID, rec)
		}
	}()
	routeEvent(ev, bead, verbose)
}
