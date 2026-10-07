// Classifying records into events: the one place an outcome is decided, and
// therefore the one place the "never claim a read" rule is enforced.
//
// Split from timeline.go (types and vocabulary) and select.go (narrowing) so
// each file stays readable in one screen.
package timeline

import (
	"fmt"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/relayctl"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// Build classifies every record into an Event. Pure: same records, same
// events, no clock and no I/O.
func Build(recs Records, presence map[string]Presence) []Event {
	events := make([]Event, 0, len(recs.Delivery)+len(recs.Audit)+len(recs.Commands))
	live, handovers := supersessionIndex(recs.Delivery)

	for i, r := range recs.Delivery {
		e := r.Entry
		ev := Event{
			AtRaw:  e.Ts,
			Source: SourceDelivery,
			Agent:  e.Agent,
			Msg:    e.Msg,
			Role:   e.Role,
			From:   e.From,
		}
		if t, ok := parseStamp(e.Ts); ok {
			ev.At, ev.HasAt = t, true
		}
		switch e.Event {
		case "rotated":
			ev.Outcome = OutcomeRotated
			ev.Detail = "the ledger rotated here (" + reasonOr(e.Reason, "unspecified") +
				") — every event older than this line is gone, so a trail that starts at this marker is not a quiet fleet"
		case "spool-failed":
			ev.Outcome = OutcomeDropped
			ev.Detail = "the append to the agent's spool FAILED — this message did not reach the agent (the failure detail is in the relay's own log, not in this identifier-only trail)"
		case "delivery-ended":
			ev.Outcome = OutcomeEnded
			ev.Detail = deliveryEndedDetail(e)
		case "spooled":
			ev.Outcome, ev.Detail = classifySpooled(i, live, handovers, e, presence[e.Agent])
		default:
			// A newer relay may record an event this reader does not know.
			// Dropping it would hide real evidence; calling it anything
			// specific would be an invention.
			ev.Outcome = OutcomeUnknown
			ev.Detail = fmt.Sprintf("unrecognised delivery event %q — this reader predates it; the record exists and is not classified", e.Event)
		}
		events = append(events, ev)
	}

	for _, a := range recs.Audit {
		ev := Event{AtRaw: a.Ts, Source: SourceAudit, Agent: a.Agent}
		if t, ok := parseStamp(a.Ts); ok {
			ev.At, ev.HasAt = t, true
		}
		switch a.Action {
		case "register":
			ev.Outcome = OutcomeEnrolled
			ev.Detail = "channel claimed by actor " + actorOr(a.Actor)
		case "unregister":
			ev.Outcome = OutcomeRetired
			ev.Detail = "channel released by actor " + actorOr(a.Actor)
		case "register-denied", "unregister-denied":
			ev.Outcome = OutcomeRefused
			ev.Detail = fmt.Sprintf("%s by actor %s — another caller held the channel; the attempt is recorded, never silent",
				a.Action, actorOr(a.Actor))
		default:
			ev.Outcome = OutcomeUnknown
			ev.Detail = fmt.Sprintf("unrecognised audit action %q — the record exists and is not classified", a.Action)
		}
		events = append(events, ev)
	}

	for _, c := range recs.Commands {
		ev := Event{Source: SourceCommand, Agent: c.Agent, AtRaw: c.StartedAt}
		if t, ok := parseStamp(c.StartedAt); ok {
			ev.At, ev.HasAt = t, true
		} else if t, ok := parseStamp(c.UpdatedAt); ok {
			ev.At, ev.HasAt, ev.AtRaw = t, true, c.UpdatedAt
		}
		ev.Outcome = OutcomeCommand
		ev.Detail = commandDetail(c)
		events = append(events, ev)
	}

	orderEvents(events)
	return events
}

// supersessionIndex finds, per (agent,msg), the index of the newest successful
// hand-over and how many hand-overs there were. Read order is write order.
func supersessionIndex(recs []DeliveryRecord) (live map[string]int, handovers map[string]int) {
	live, handovers = map[string]int{}, map[string]int{}
	for i, r := range recs {
		if r.Entry.Msg == "" || r.Entry.Event != "spooled" {
			continue
		}
		k := r.Entry.Agent + "\x00" + r.Entry.Msg
		handovers[k]++
		live[k] = i
	}
	return live, handovers
}

// classifySpooled turns one successful hand-over into the strongest claim the
// spool can support. It is the only place Queued is produced, and it never
// upgrades: an unreadable spool is Unknown, not Queued.
func classifySpooled(i int, live, handovers map[string]int, e relayctl.DeliveryEntry, p Presence) (Outcome, string) {
	k := e.Agent + "\x00" + e.Msg
	if last, ok := live[k]; ok && last != i {
		return OutcomeSuperseded, fmt.Sprintf(
			"an earlier hand-over of this message id — it was handed over %d time(s) and only the newest one is the live delivery (a channel replay, not a second message)",
			handovers[k])
	}
	who := "the agent"
	if p.Retired {
		// Retired spools are read too: a channel that ended is exactly when
		// its leftover lines matter.
		who = "the agent's retired spool"
	}
	if !p.Known {
		return OutcomeUnknown, "the relay spooled it, but the spool could not be read (" + p.Reason +
			"), so whether the line is still waiting is not observable from here"
	}
	if n := p.IDs[e.Msg]; n > 0 {
		return OutcomeQueued, "still in " + who + "'s spool — nothing in this fleet acknowledges a read, so this is queued, not delivered"
	}
	return OutcomeLeftSpool, "no longer in " + who + "'s spool — read or pruned, and nothing in this fleet records which"
}

func deliveryEndedDetail(e relayctl.DeliveryEntry) string {
	reason := reasonOr(e.Reason, "unspecified")
	lines := "an unrecorded number of lines"
	if e.SpoolLines != nil {
		lines = fmt.Sprintf("%d line(s)", *e.SpoolLines)
	}
	return fmt.Sprintf("the channel stopped being polled — reason=%s; %s were still in the spool at that moment, unproven-consumed", reason, lines)
}

func commandDetail(c wire.CommandInvocation) string {
	parts := []string{c.Verb + " → " + c.State}
	if c.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit=%d", *c.ExitCode))
	}
	if c.Outcome != "" {
		parts = append(parts, "outcome="+c.Outcome)
	}
	switch {
	case c.DurationMs > 0:
		parts = append(parts, "took="+formatDuration(time.Duration(c.DurationMs)*time.Millisecond))
	case c.State == "running":
		// A running record has no duration yet; its age is the honest number.
		if t, ok := parseStamp(c.StartedAt); ok {
			parts = append(parts, "running-for="+formatDuration(time.Since(t)))
		}
	}
	return strings.Join(parts, " · ")
}
