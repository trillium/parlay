// The relay's control-plane claim trail as evidence about delivery PATHS.
//
// `unhanded` is the one verdict in this vocabulary that accuses something —
// "NOTHING picked this message up" — so it may only be stated when the relay
// can be shown to have been the delivery path for that channel at that time.
// The relay's audit trail is the durable proof either way: it writes `register`
// when a monitor claims a channel and `unregister` when the claim is released,
// so a claim interval says the relay was polling, and the complete absence of a
// claim says it was never the path.
//
// That distinction is not cosmetic. `parlay listen --legacy-poll` polls the
// chat server directly with no relay involved (docs/monitor.md), and what such
// a poll consumes leaves NO record in this fleet at all — no spool line, no
// ledger hand-over, no claim. Without this evidence, every message an agent
// received that way would be reported as lost.
package timeline

import (
	"fmt"
	"time"
)

// Claim is one interval in which a channel was claimed at the relay: from a
// `register` (or a `register-denied`, which proves a claim existed because
// another caller held the channel) up to the `unregister` that released it.
//
// HasTo false with Undated false means the claim is still open — the trail's
// last word on that channel. Undated marks a claim one of whose ends this
// reader could not place in time; such a claim can neither cover a message nor
// be ruled out, so it suppresses the verdict rather than deciding it.
type Claim struct {
	From    time.Time
	HasFrom bool
	To      time.Time
	HasTo   bool
	Undated bool
}

// EnrollmentEvidence is the relay's claim trail as read off disk, per channel.
// Its zero value means "not usable", so a caller that forgets to supply it gets
// no accusation rather than a false one — the same rule HandoverEvidence
// follows, and for the same reason.
type EnrollmentEvidence struct {
	// Read is true when audit.log was read and parsed. False with a Reason is
	// an older relay, another runtime dir, or an unreadable file: all three
	// mean the claim history is unknown, never that it is empty.
	Read bool
	// Truncated is true when the file exceeded the reader's cap, so the OLDEST
	// claims are missing and a missing claim stops being evidence.
	Truncated bool
	// Reason names why the trail could not be used, when !Read.
	Reason string
	// Claims is the claim intervals per channel, oldest first.
	Claims map[string][]Claim
}

// ClaimAt answers the only question the unhanded verdict may ask: is the relay
// known to have been the delivery path for this channel at this instant?
//
// claimed true means the trail proves a claim covering the time. claimed false
// always carries the sentence explaining why the absence of a hand-over line is
// not evidence — an unreadable trail, an undated claim, a claim that had
// already ended, or a channel the relay never claimed at all.
func (e EnrollmentEvidence) ClaimAt(agent string, at time.Time) (bool, string) {
	switch {
	case !e.Read:
		return false, fmt.Sprintf(
			"The relay's claim trail (audit.log) could not be read (%s), so whether the relay was ever the delivery path for this channel is unknown — a missing hand-over line is not evidence",
			reasonOr(e.Reason, "absent, or a different runtime dir"))
	case e.Truncated:
		return false, "The relay's claim trail (audit.log) exceeded this reader's cap, so its OLDEST claims are not in it — a missing claim for this channel is not evidence"
	}

	var lastEnd time.Time
	hasEnd := false
	for _, c := range e.Claims[agent] {
		if c.Undated || !c.HasFrom {
			return false, "The relay's claim trail (audit.log) holds a claim for this channel whose time cannot be read, so whether it covers this message is unknown — a missing hand-over line is not evidence"
		}
		if c.HasTo {
			lastEnd, hasEnd = c.To, true
		}
		if !at.Before(c.From) && (!c.HasTo || !at.After(c.To)) {
			return true, ""
		}
	}
	if hasEnd {
		return false, fmt.Sprintf(
			"The relay's claim trail (audit.log, read in full) shows the relay's last claim on this channel ended at %s, before this message was recorded — the relay was not the delivery path for it, so a missing hand-over line is not evidence: nothing in this fleet records what has no relay behind it, including a direct poll (`parlay listen --legacy-poll`)",
			lastEnd.UTC().Format(time.RFC3339))
	}
	return false, "The relay's claim trail (audit.log, read in full) holds NO claim for this channel at all — the relay was never the delivery path for it, so no hand-over line was ever going to exist. An agent can receive messages without the relay (`parlay listen --legacy-poll` polls the chat server directly), and what such a poll consumed is recorded nowhere in this fleet"
}
