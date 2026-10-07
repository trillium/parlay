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
// events, no clock of its own and no I/O (the one clock it uses is the caller's,
// in Records.Handover.Now, so a verdict about "too recent to judge" is
// reproducible in a test).
func Build(recs Records, presence map[string]Presence) []Event {
	events := make([]Event, 0, len(recs.Delivery)+len(recs.Audit)+len(recs.Commands)+len(recs.History))
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
		case "started":
			ev.Outcome = OutcomeStarted
			ev.Detail = "the relay process started here: it took its control socket and began serving. Deliveries cannot flow from a relay that is not running, so a gap between two of these lines is a RESTART, not a quiet fleet — and a burst of them is a crash loop. It names no channel: what came back is the `resumed` rows"
		case "resumed":
			ev.Outcome = OutcomeResumed
			ev.Detail = "the relay resumed polling this channel at its start, from the spool it found on disk — so this channel HAD A POLL LOOP from this instant. It is not proof the agent was listening, and not proof any queued line was read"
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

	// The server's own history: what was persisted, which is the only record
	// that exists when the relay never touched a message. It is built last and
	// classified against the delivery events gathered above, so a message that
	// WAS handed over reads as Recorded pointing at the hand-over rather than
	// as a second, competing claim.
	handed := handedOver(recs.Delivery)
	for _, h := range recs.History {
		ev := Event{AtRaw: h.Ts, Source: SourceHistory, Agent: h.Channel, Msg: h.ID, Role: h.Role, From: h.From}
		if t, ok := parseStamp(h.Ts); ok {
			ev.At, ev.HasAt = t, true
		}
		ev.Outcome, ev.Detail = classifyHistory(ev, handed, recs.Handover)
		events = append(events, ev)
	}

	orderEvents(events)
	return events
}

// handedOver indexes every message id the delivery trail mentions, successful
// or not: a spool-failed means the relay DID try, so the message was handed to
// it and "nothing picked it up" would be the wrong story (that line is the
// dropped outcome).
func handedOver(recs []DeliveryRecord) map[string]bool {
	out := map[string]bool{}
	for _, r := range recs {
		if r.Entry.Msg != "" {
			out[r.Entry.Agent+"\x00"+r.Entry.Msg] = true
		}
	}
	return out
}

// classifyHistory decides what the server's own record can honestly claim about
// delivery. The default is Recorded — the weak, true statement — and Unhanded
// is only reachable when every guard in HandoverEvidence says the absence of a
// hand-over is evidence. Each guard gets its own sentence, so an operator can
// see WHY the fleet is not accusing the relay.
func classifyHistory(ev Event, handed map[string]bool, he HandoverEvidence) (Outcome, string) {
	const base = "the chat server persisted this message on this channel"
	if handed[ev.Agent+"\x00"+ev.Msg] {
		return OutcomeRecorded, base + "; the relay's own hand-over line for this message is in this timeline — that line, not this one, says what happened next"
	}
	switch {
	case !he.Read:
		return OutcomeRecorded, base + ". No hand-over line for it is here and NO delivery trail could be read, so whether the relay ever took it is unknown — not absent"
	case !he.Complete:
		return OutcomeRecorded, base + ". The delivery trail was read but " + reasonOr(he.Reason, "it is not a complete record of this window") + " — a missing hand-over line is not evidence"
	case he.Now.IsZero():
		return OutcomeRecorded, base + ". This reader was not told the time, so it cannot tell whether the relay has had a chance to poll it — absence is not evidence"
	case !ev.HasAt:
		return OutcomeRecorded, base + ". Its stamp does not parse, so it cannot be placed in time, and the relay may simply not have polled it yet — absence is not evidence"
	case he.Grace > 0 && he.Now.Sub(ev.At) < he.Grace:
		return OutcomeRecorded, fmt.Sprintf("%s %s ago — younger than the hand-over window (%s) the relay is allowed before silence means something, so this is not counted as unhanded",
			base+". It was recorded", formatDuration(he.Now.Sub(ev.At)), formatDuration(he.Grace))
	case !he.HasCoveredFrom:
		return OutcomeRecorded, base + ". The delivery trail holds no dated line to date itself from, so it cannot be asked about this message"
	case ev.At.Before(he.CoveredFrom):
		return OutcomeRecorded, fmt.Sprintf("%s. The delivery trail begins at %s, AFTER this message, so it cannot say whether the relay handled it",
			base, he.CoveredFrom.UTC().Format(time.RFC3339))
	default:
		// The trail's coverage is not enough on its own: a hand-over is also
		// absent for a channel the relay was never the delivery path for. That
		// is what a direct poll (`parlay listen --legacy-poll`) looks like from
		// here, and accusing it of losing the message would be the exact false
		// verdict this whole rule exists to prevent.
		if claimed, why := he.Enrollment.ClaimAt(ev.Agent, ev.At); !claimed {
			return OutcomeRecorded, base + ". " + why
		}
		return OutcomeUnhanded, base + " and the relay's delivery trail — read in full, with no rotation — holds no hand-over for it around the time its claim on this channel covers: NOTHING picked this message up. Either the relay polls a different chat server, or the hand-over failed without leaving a line. The message is still in the agent's history, so it can be resent"
	}
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
