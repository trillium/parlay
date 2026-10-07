// Package timeline turns the fleet's durable records into ONE ordered answer
// to "what happened?" — messages, agent lifecycle, delivery outcomes and
// commands on a single axis, each event carrying an outcome that says which
// of delivered/queued/dropped the fleet can actually prove.
//
// The problem it exists to fix: every record was already durable, and none of
// them was queryable together. The relay's delivery ledger knew when a message
// reached a spool. Its audit log knew when a channel was claimed and when it
// was retired. The server's command registry knew what an invocation did and
// how it ended. Answering "what happened to agent X between 08:00 and 09:00"
// meant reading three files and one HTTP route by hand and interleaving them
// by eye — and reading source to know what their vocabularies meant.
//
// # The one rule
//
// An outcome is a claim about what the fleet CAN prove, and the vocabulary is
// deliberately smaller than the obvious one:
//
//   - Nothing in this fleet acknowledges that a message was READ. There is no
//     receipt, no cursor advance the sender can see, and the relay's poll loop
//     never learns what the monitor tailing the spool has consumed. So there
//     is no `delivered` outcome, and Queued never means delivered. A surface
//     that reported `spooled` as `delivered` would reintroduce exactly the lie
//     this package exists to remove.
//   - Absent evidence is Unknown with a reason, never a default. A spool that
//     could not be read does not make a message "gone"; it makes its state
//     unknowable, and that is what it says.
//   - The server's own history is a SEPARATE source, because it answers a
//     question the relay's trail cannot: whether the fleet ever had the
//     message at all. A recorded message with a hand-over line is Recorded
//     (the hand-over line, not this one, says what happened next); a recorded
//     message with no hand-over anywhere is Unhanded — but only when the
//     caller can prove the trail is a complete record covering that message's
//     time. Every weaker case stays Recorded with the guard it failed, because
//     "nothing picked it up" is an accusation and an incomplete trail cannot
//     support it.
//
// # Superseded
//
// A message id handed over twice — a relay restart whose resume cursor could
// not be seeded from a spool whose last line was a non-chat event replays the
// channel, which is a real and observed shape — produces two `spooled` lines
// for one message. Only the newest hand-over is the live one; the earlier
// lines are Superseded, and they say what superseded them. Supersession is
// computed in READ order (the rotated generation first, then the active file),
// which is the order the relay wrote them: an append-only trail's read order
// is its write order, so a stamp that fails to parse cannot reorder history.
//
// Everything here is pure: Build and Select take data and return events.
// Every read lives in the caller, which is what makes each degraded mode
// testable without standing up a fleet.
package timeline

import (
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/chathistory"
	"github.com/trillium/parlay/tools/cli/internal/relayctl"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// Outcome is what the fleet can prove about one event.
type Outcome string

const (
	// OutcomeQueued: the relay appended this message to the agent's spool and
	// the line is STILL there. Queued, not delivered — nothing acknowledges a
	// read, so "still in the spool" is the strongest true statement.
	OutcomeQueued Outcome = "queued"
	// OutcomeLeftSpool: it was spooled and the spool no longer holds that line.
	// Read and pruned are indistinguishable from here, and the detail says so.
	OutcomeLeftSpool Outcome = "left-spool"
	// OutcomeDropped: the spool append FAILED, so the message never reached
	// the agent. The only outcome in this vocabulary that is unambiguously bad.
	OutcomeDropped Outcome = "dropped"
	// OutcomeSuperseded: an earlier hand-over of a message id that was handed
	// over again later. Not a loss — the live hand-over is a separate event.
	OutcomeSuperseded Outcome = "superseded"
	// OutcomeEnded: the channel stopped being polled, with the reason and how
	// many lines were still in the spool at that moment.
	OutcomeEnded Outcome = "ended"
	// OutcomeRotated: the ledger rotated here. Everything older is gone.
	OutcomeRotated Outcome = "rotated"
	// OutcomeRecorded: the CHAT SERVER persisted this message on this channel.
	// It is the server's own history, not a delivery: a recorded message can sit
	// unhanded forever, and the relay's hand-over (if any) is a separate event.
	OutcomeRecorded Outcome = "recorded"
	// OutcomeUnhanded: the server recorded the message, the relay is shown by
	// its own claim trail to have been the DELIVERY PATH for this channel at
	// that time, and the delivery trail — read in full, starting before this
	// message — holds no hand-over for it. Nothing picked it up. Only produced
	// when absence IS evidence: see HandoverEvidence and EnrollmentEvidence.
	// Everything weaker stays Recorded with the reason.
	OutcomeUnhanded Outcome = "unhanded"
	// OutcomeUnknown: something was recorded and this reader cannot classify
	// it (an unreadable spool, an event name from a newer relay). Always
	// carries a reason; never invented into a healthier one.
	OutcomeUnknown Outcome = "unknown"
	// OutcomeEnrolled / OutcomeRetired / OutcomeRefused: relay control-plane
	// actions — a channel claimed, released, or a takeover refused.
	OutcomeEnrolled Outcome = "enrolled"
	OutcomeRetired  Outcome = "retired"
	OutcomeRefused  Outcome = "refused"
	// OutcomeCommand: one live-command registry invocation, with its state,
	// exit code, outcome token and timing.
	OutcomeCommand Outcome = "command"
)

// Outcomes is every member, in the order the help text documents them. A
// caller validating --outcome walks this rather than a second hand-written
// list, so the two cannot drift.
var Outcomes = []Outcome{
	OutcomeRecorded, OutcomeUnhanded, OutcomeQueued, OutcomeLeftSpool, OutcomeDropped,
	OutcomeSuperseded, OutcomeEnded, OutcomeRotated, OutcomeEnrolled, OutcomeRetired,
	OutcomeRefused, OutcomeCommand, OutcomeUnknown,
}

// ParseOutcome resolves one --outcome token, case-insensitively.
func ParseOutcome(s string) (Outcome, bool) {
	for _, o := range Outcomes {
		if strings.EqualFold(string(o), strings.TrimSpace(s)) {
			return o, true
		}
	}
	return "", false
}

// Source is which record an event came from. Kept out of the render and in the
// data so a --json consumer can pivot on it without re-parsing detail text.
type Source string

const (
	SourceDelivery Source = "delivery" // the relay's data-plane ledger
	SourceAudit    Source = "audit"    // the relay's control-plane audit log
	SourceCommand  Source = "command"  // the server's live-command registry
	SourceHistory  Source = "history"  // the server's own persisted chat history
)

// Event is one thing that happened at one time.
type Event struct {
	At    time.Time
	HasAt bool // false = the record's stamp is absent or unparseable
	// AtRaw is the stamp as written, kept so a reader can see what failed.
	AtRaw   string
	Outcome Outcome
	Source  Source
	Agent   string
	Msg     string
	Role    string
	From    string
	Detail  string
}

// DeliveryRecord is one delivery-ledger entry plus the generation it came
// from. Rotated entries are older than the active file's.
type DeliveryRecord struct {
	Entry   relayctl.DeliveryEntry
	Rotated bool
}

// Presence is what this reader knows about one agent's spool, which is what
// lets a spooled message be reported as still queued. Known=false is the
// honest answer for a spool that is missing, retired-but-unread, or
// unreadable — see SpoolPresence.
type Presence struct {
	Known     bool
	Reason    string // why not known, when !Known
	IDs       map[string]int
	Retired   bool
	Truncated bool
}

// Records is every raw record a caller managed to gather. An empty slice means
// the source was read and had nothing; the caller reports "could not ask"
// separately, because the two are different answers.
type Records struct {
	Delivery []DeliveryRecord
	Audit    []relayctl.AuditEntry
	Commands []wire.CommandInvocation
	History  []chathistory.Record

	// Handover is what the caller can prove about the DELIVERY TRAIL's coverage.
	// It is the only thing that lets an absent hand-over for a recorded message
	// be stated as fact rather than guessed; its zero value means "not usable",
	// so a caller that forgets it gets no verdict instead of a false one.
	Handover HandoverEvidence
}

// HandoverEvidence is the delivery trail's coverage, from the caller's own read.
// Every field is a guard against a different false accusation:
//
//   - Read false: no trail at all (an older relay predates the ledger), so a
//     missing hand-over says nothing.
//   - Complete false: the trail was read but rotated or truncated, so older
//     events are gone and a missing id is not evidence.
//   - !HasCoveredFrom: the trail holds no timestamped line to date it from, so
//     it cannot be asked about any particular message.
//   - CoveredFrom: a message older than the trail's own first line predates
//     the record — the ledger's absence of it is not evidence about it.
//   - Grace: how long the relay is allowed before silence becomes a verdict.
//     A message recorded two seconds ago is not "unhanded"; it has not been
//     polled yet.
//   - Now: the clock the caller read at. Zero disables the verdict entirely,
//     because an age cannot be computed from a clock nobody supplied.
//
// Coverage of the trail is only half of it: a hand-over is also absent for a
// channel the relay was never polling, which is the normal shape of the
// `--legacy-poll` delivery path. Enrollment is the other half, and its zero
// value (not read) suppresses the verdict too.
type HandoverEvidence struct {
	Read           bool
	Complete       bool
	Reason         string
	HasCoveredFrom bool
	CoveredFrom    time.Time
	Grace          time.Duration
	Now            time.Time
	Enrollment     EnrollmentEvidence
}
