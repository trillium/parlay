// Rendering for `parlay timeline`: the human view and the --json document.
//
// The layout is fixed and columnar so that a diff of two runs is readable and
// a line can be grepped by outcome. Every line an operator reads is built here
// from an Event that already carries its classification — the renderer decides
// nothing about what happened, only how it is shown.
package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/timeline"
)

// renderTimeline writes the text view: a header naming the question that was
// asked, the events in chronological order, and a sources footer naming every
// record that did or did not answer.
func renderTimeline(g timelineGather, ta timelineArgs, kept []timeline.Event, matched int) {
	renderTimelineTo(os.Stdout, g, ta, kept, matched, time.Now())
}

func renderTimelineTo(w io.Writer, g timelineGather, ta timelineArgs, kept []timeline.Event, matched int, now time.Time) {
	fmt.Fprintf(w, "parlay timeline — oldest first; %s\n", shownOf(len(kept), matched, ta.limit))
	if q := questionLine(ta, now); q != "" {
		fmt.Fprintf(w, "  asked: %s\n", q)
	}
	fmt.Fprintf(w, "  runtime %s · server %s\n", g.Runtime, g.Server)
	fmt.Fprintln(w)

	if len(kept) == 0 {
		fmt.Fprintf(w, "  no event matched. %s\n", emptyMeaning(g))
	} else {
		for _, e := range kept {
			fmt.Fprintf(w, "%s  %-8s  %-11s  %-16s  %s\n", stamp(e), age(e, now), e.Outcome, subject(e), detail(e))
		}
		if c := timeline.Counts(kept); len(c) > 0 {
			fmt.Fprintf(w, "\n  shown: %s\n", strings.Join(c, " · "))
		}
	}

	fmt.Fprintln(w, "\nsources")
	for _, s := range g.Sources {
		line := fmt.Sprintf("  %-22s %s", s.Name+" ("+s.State+")", s.Detail)
		if s.Path != "" {
			line += " · " + s.Path
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w, "\nNothing here is a claim that a message was READ: this fleet has no read receipt anywhere, so\n"+
		"`queued` means the line is still in the spool and says nothing more. `recorded` is the chat server's\n"+
		"own history, not a delivery; `unhanded` is the one verdict made from it, and only when the delivery\n"+
		"trail can be shown to be a complete record covering that message.")
}

// shownOf phrases the count honestly: "12 shown of 340 matching" when a limit
// cut the list, and "0 of 0 matching" only when the question really matched
// nothing. A truncated list that reads like a complete one is the failure this
// line exists to prevent.
func shownOf(shown, matched, limit int) string {
	switch {
	case matched == 0:
		return "0 event(s) matched"
	case limit > 0 && matched > shown:
		return fmt.Sprintf("newest %d of %d matching event(s) (--limit 0 shows all)", shown, matched)
	default:
		return fmt.Sprintf("%d matching event(s)", matched)
	}
}

// questionLine restates the filters, so a reader can tell a quiet fleet from
// their own narrow question.
func questionLine(ta timelineArgs, now time.Time) string {
	parts := []string{}
	if ta.agent != "" {
		parts = append(parts, "agent "+ta.agent)
	}
	if ta.window.HasSince {
		parts = append(parts, "since "+ta.sinceRaw+" ("+ta.window.Since.UTC().Format(time.RFC3339)+")")
	}
	if ta.window.HasUntil {
		parts = append(parts, "until "+ta.untilRaw+" ("+ta.window.Until.UTC().Format(time.RFC3339)+")")
	}
	if len(ta.outcomes) > 0 {
		names := make([]string, 0, len(ta.outcomes))
		for _, o := range timeline.Outcomes {
			if ta.outcomes[o] {
				names = append(names, string(o))
			}
		}
		parts = append(parts, "outcome "+strings.Join(names, ","))
	}
	if len(parts) == 0 {
		return "everything the records still hold"
	}
	return strings.Join(parts, " · ")
}

// emptyMeaning says WHY nothing matched, which is the difference between a
// fleet that did nothing and a reader that could not look.
func emptyMeaning(g timelineGather) string {
	if !g.answered {
		return "No record answered at all, so an empty timeline means nothing was observable — see sources."
	}
	return "At least one record answered and held no event for this question, so the fleet really is quiet over it (see sources for what was read)."
}

// stamp is the time column. An event whose stamp did not parse shows "?" — the
// record is real, its time is not, and inventing one would be a fabrication.
func stamp(e timeline.Event) string {
	const width = 20
	if !e.HasAt {
		return "?" + strings.Repeat(" ", width-1)
	}
	return e.At.UTC().Format(time.RFC3339)
}

func age(e timeline.Event, now time.Time) string {
	if !e.HasAt {
		return "?"
	}
	d := now.Sub(e.At)
	switch {
	case d < 0:
		return "in the future"
	default:
		return timeline.FormatDuration(d) + " ago"
	}
}

// subject is the column a reader scans for "which agent": the agent id, or an
// explicit "-" for a record that names none (a rotated marker is fleet-wide).
func subject(e timeline.Event) string {
	if strings.TrimSpace(e.Agent) == "" {
		return "-"
	}
	return e.Agent
}

// detail is the classified sentence, prefixed with the message reference when
// there is one. The id and role are identifiers the trails already carry; the
// message body is deliberately absent from every record this reads.
func detail(e timeline.Event) string {
	d := e.Detail
	ref := ""
	switch {
	case e.Msg != "" && e.Role != "":
		ref = "msg " + e.Msg + " (" + e.Role + ")"
	case e.Msg != "":
		ref = "msg " + e.Msg
	}
	if e.From != "" {
		ref += " from " + e.From
	}
	if ref != "" {
		return ref + " — " + d
	}
	return d
}

// eventJSON is one event in --json. atKnown is separate from at because an
// absent stamp is not midnight and must not decode into one.
type eventJSON struct {
	At      string           `json:"at,omitempty"`
	AtKnown bool             `json:"atKnown"`
	AtRaw   string           `json:"atRaw,omitempty"`
	Outcome timeline.Outcome `json:"outcome"`
	Source  timeline.Source  `json:"source"`
	Agent   string           `json:"agent,omitempty"`
	Msg     string           `json:"msg,omitempty"`
	Role    string           `json:"role,omitempty"`
	From    string           `json:"from,omitempty"`
	Detail  string           `json:"detail"`
}

// timelineEnvelope is the --json document. shown/matched/limit are all present
// on every run so a consumer can tell a complete answer from a truncated one
// without inferring it from two array lengths.
type timelineEnvelope struct {
	OK      bool         `json:"ok"`
	Now     string       `json:"now"`
	Runtime string       `json:"runtime"`
	Server  string       `json:"server"`
	Shown   int          `json:"shown"`
	Matched int          `json:"matched"`
	Limit   int          `json:"limit"`
	Events  []eventJSON  `json:"events"`
	Sources []sourceNote `json:"sources"`
}

func emitTimelineJSON(g timelineGather, ta timelineArgs, kept []timeline.Event, matched int) {
	emitTimelineJSONTo(os.Stdout, g, ta, kept, matched, time.Now())
}

func emitTimelineJSONTo(w io.Writer, g timelineGather, ta timelineArgs, kept []timeline.Event, matched int, now time.Time) {
	doc := timelineEnvelope{
		OK:      true,
		Now:     now.UTC().Format(time.RFC3339),
		Runtime: g.Runtime,
		Server:  g.Server,
		Shown:   len(kept),
		Matched: matched,
		Limit:   ta.limit,
		Events:  make([]eventJSON, 0, len(kept)),
		Sources: g.Sources,
	}
	for _, e := range kept {
		row := eventJSON{
			AtKnown: e.HasAt,
			AtRaw:   e.AtRaw,
			Outcome: e.Outcome,
			Source:  e.Source,
			Agent:   e.Agent,
			Msg:     e.Msg,
			Role:    e.Role,
			From:    e.From,
			Detail:  e.Detail,
		}
		if e.HasAt {
			row.At = e.At.UTC().Format(time.RFC3339)
		}
		doc.Events = append(doc.Events, row)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		// A write failure to stdout is not something a timeline can report
		// through itself; say it on stderr and let the exit code stand.
		fmt.Fprintf(os.Stderr, "parlay timeline: writing --json output failed: %v\n", err)
	}
}
