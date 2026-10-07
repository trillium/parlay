// parlay timeline — ONE ordered answer to "what happened", assembled from
// every durable record the fleet already keeps.
//
// The problem this exists to fix: the relay's delivery ledger knew what it
// handed to a spool, its audit log knew when a channel was claimed and
// released, and the server's command registry knew what an invocation did —
// three records, three shapes, three places, and no way to read them on one
// axis. "What happened to crew-1 between 08:00 and 09:00" meant reading two
// files and one HTTP route by hand and interleaving them by eye, then reading
// source to know what their vocabularies meant. `parlay explain` answers the
// question about ONE agent's current state; this answers the question about a
// WINDOW of what actually happened.
//
// # What it will not say
//
// Nothing in this fleet acknowledges that a message was READ. There is no
// receipt, no consumer cursor the sender can see, and the relay's poll loop
// never learns what the monitor tailing the spool consumed. So there is no
// `delivered` outcome here, and `queued` never gets upgraded into one. The
// strongest claim the data supports is "spooled, and the line is still in the
// spool", which is what `queued` says. See package internal/timeline.
//
// # Why it works when the fleet is broken
//
// The relay's trails are files and are read off disk, so they answer while the
// relay itself is dead — which is the usual reason someone is asking. Every
// read that could not happen is named in the `sources` footer with what it
// would have answered, and the exit code is 0 whenever any record answered,
// however bad the news.
//
// Read-only, like every diagnostic here: it never sends, enrolls, or retires.
// Go-only, no TS port (same as explain/stale/merge-gate): the parity harness
// was retired in T-08.
package commands

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/relayctl"
	"github.com/trillium/parlay/tools/cli/internal/timeline"
)

// ExitTimelineNothing is the one non-zero outcome: no record answered at all,
// so there is nothing to report and "no events" would understate it. Any
// single source answering (even with bad news) exits 0.
const ExitTimelineNothing = config.ExitRuntime

// timelineDefaultLimit is how many events are shown without --limit. The
// window is widened by --since, so the default is a "what just happened"
// read; --limit 0 shows everything the records hold.
const timelineDefaultLimit = 50

// timelineArgs is the parsed, validated command line.
type timelineArgs struct {
	agent    string
	outcomes map[timeline.Outcome]bool
	window   timeline.Window
	limit    int
	asJSON   bool
	sinceRaw string
	untilRaw string
}

// Timeline implements `parlay timeline [<agent-id>] [flags]`.
func Timeline(argv []string) {
	if helpWanted("timeline", argv) {
		return
	}
	ta := parseTimelineArgs(argv)
	g := gatherTimeline(ta.agent)

	events := timeline.Build(g.Records, g.Presence)
	filter := timeline.Filter{Agent: ta.agent, Outcomes: ta.outcomes, Window: ta.window}
	kept, matched := timeline.Select(events, filter, ta.limit)

	if ta.asJSON {
		emitTimelineJSON(g, ta, kept, matched)
	} else {
		renderTimeline(g, ta, kept, matched)
	}

	if !g.answered {
		fmt.Fprintf(os.Stderr,
			"parlay timeline: nothing was observable — no delivery ledger, no audit log, the relay at %s did not answer, and no command registry at %s\n",
			relayctl.SockPath(), g.Server)
		httpc.Exit(ExitTimelineNothing)
	}
}

// parseTimelineArgs validates the command line and dies with a usage error on
// anything it cannot honour. It never performs I/O.
func parseTimelineArgs(argv []string) timelineArgs {
	r := args.Parse("timeline", argv,
		[]string{"--json"},
		[]string{"--agent", "--channel", "--since", "--until", "--outcome", "--limit"})

	ta := timelineArgs{outcomes: map[timeline.Outcome]bool{}, limit: timelineDefaultLimit}

	agent, hasAgent := r.String("--agent")
	channel, hasChannel := r.String("--channel")
	if hasAgent && hasChannel && strings.TrimSpace(agent) != strings.TrimSpace(channel) {
		httpc.Die("parlay timeline: --agent and --channel name the same thing (a channel id IS an agent id here) — give it once, or give the same value", config.ExitUsage)
	}
	ta.agent = strings.TrimSpace(firstNonEmpty(agent, channel))
	if len(r.Positionals) > 1 {
		httpc.Die("parlay timeline: at most one agent id (e.g. 'parlay timeline crew-1')", config.ExitUsage)
	}
	if len(r.Positionals) == 1 {
		p := strings.TrimSpace(r.Positionals[0])
		if ta.agent != "" && ta.agent != p {
			httpc.Die(fmt.Sprintf("parlay timeline: agent named twice and differently (%q and %q) — name it once", ta.agent, p), config.ExitUsage)
		}
		ta.agent = p
	}

	now := time.Now()
	if raw, ok := r.String("--since"); ok {
		t, present, why := parseWhen(raw, now)
		if !present {
			httpc.Die("parlay timeline: --since "+why+" (got "+strings.TrimSpace(raw)+")", config.ExitUsage)
		}
		ta.window.Since, ta.window.HasSince, ta.sinceRaw = t, true, strings.TrimSpace(raw)
	}
	if raw, ok := r.String("--until"); ok {
		t, present, why := parseWhen(raw, now)
		if !present {
			httpc.Die("parlay timeline: --until "+why+" (got "+strings.TrimSpace(raw)+")", config.ExitUsage)
		}
		ta.window.Until, ta.window.HasUntil, ta.untilRaw = t, true, strings.TrimSpace(raw)
	}
	if ta.window.HasSince && ta.window.HasUntil && ta.window.Until.Before(ta.window.Since) {
		httpc.Die("parlay timeline: --until is before --since, which matches nothing by construction", config.ExitUsage)
	}

	if raw, ok := r.String("--outcome"); ok {
		for _, tok := range strings.Split(raw, ",") {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			o, valid := timeline.ParseOutcome(tok)
			if !valid {
				httpc.Die(fmt.Sprintf("parlay timeline: unknown outcome %q — pick from %s", tok, outcomeList()), config.ExitUsage)
			}
			ta.outcomes[o] = true
		}
		if len(ta.outcomes) == 0 {
			httpc.Die("parlay timeline: --outcome was given with no outcome names", config.ExitUsage)
		}
	}

	if raw, ok := r.String("--limit"); ok {
		n, err := parseLimit(raw)
		if err != nil {
			httpc.Die("parlay timeline: "+err.Error()+" (got "+strings.TrimSpace(raw)+")", config.ExitUsage)
		}
		ta.limit = n
	}
	ta.asJSON = r.Bool("--json")
	return ta
}

// parseLimit accepts a count of shown events. 0 means every match, which is
// the escape hatch for a script that wants the whole trail.
func parseLimit(raw string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &n); err != nil {
		return 0, fmt.Errorf("--limit wants a non-negative count of events shown (0 = all)")
	}
	if n < 0 {
		return 0, fmt.Errorf("--limit cannot be negative")
	}
	return n, nil
}

func outcomeList() string {
	parts := make([]string, 0, len(timeline.Outcomes))
	for _, o := range timeline.Outcomes {
		parts = append(parts, string(o))
	}
	return strings.Join(parts, ",")
}
