// Package inputlog records what happened to operator input as it moved
// through parlay's input seam — the hops between "the phone sent something"
// and "an agent was handed it".
//
// Why this exists: four different failures on that seam look identical from
// the panel. The speech recogniser mishears a sentence; the relay never
// forwards it; the command parses but names no agent; the phone never sent
// it. The chat log cannot tell them apart, because a message that was
// accepted and queued is byte-identical on disk to one that was delivered,
// and one that was refused by the intake never reaches disk at all. This
// ledger is the missing durable half: one append-only record per hop, keyed
// by the input's own id, so a view can name which failure happened and a
// replay can show every hop an input made.
//
// Three rules the vocabulary enforces, each because the alternative is a
// view that lies:
//
//  1. A hop that did not succeed must carry a reason. Record() rejects an
//     event whose Class is anything but ok and whose Reason is empty. An
//     input event that failed with no recorded reason is a defect in the
//     tooling, so it is counted (Stats.Rejected) rather than quietly stored
//     as an unexplained failure.
//  2. "No confidence reported" is its own state (ClassConfidenceUnknown),
//     never folded into "confident". Today no intake surface in this repo
//     reports a recogniser confidence, so this is the state every dictation
//     actually lands in, and a view that showed it as ok would be inventing
//     evidence.
//  3. The ledger stores no message text. It records ids, stages, classes,
//     short reason tokens, and numbers — never the body of what the operator
//     said. That is deliberate: the message log already holds the text and
//     its own retention posture, and observability must not become a second
//     copy of it.
//
// Nothing here sits in the delivery path. See log.go's Log.Record.
package inputlog

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Stage is the hop in the input seam an event was recorded at. Every stage
// describes what had happened to the input at that moment, not what it
// eventually became — an input can have a received event at one stage and
// never get a later one, which is exactly the silence this ledger exists to
// make legible.
const (
	// StageReceived: an intake surface accepted the input. This is the
	// phone/CLI/hook boundary, before any interpretation.
	StageReceived = "received"
	// StageInterpreted: the intake decided what the input IS — free text,
	// a parsed command, or unusable speech. A recogniser error and a
	// low-confidence transcript are both recorded here.
	StageInterpreted = "interpreted"
	// StageRouted: a destination was chosen for the input, or routing
	// failed to choose one (ClassNoMatch, ClassRefused).
	StageRouted = "routed"
	// StageQueued: the input is durably held awaiting pickup.
	StageQueued = "queued"
	// StageDelivered: a listener was handed the input.
	StageDelivered = "delivered"
	// StageHeld: the input was deliberately not routed, pending an operator
	// decision (a confidence or provenance threshold).
	StageHeld = "held"
	// StageSuperseded: a later input replaced this one before it was acted
	// on.
	StageSuperseded = "superseded"
)

// Class is the honest outcome of the hop. Exactly one class per event; the
// zero value is invalid so a caller that forgot to classify is rejected
// rather than stored as a success.
const (
	// ClassOK: the hop did what it was supposed to.
	ClassOK = "ok"
	// ClassRecogniserError: the speech recogniser reported that it could
	// not transcribe the input at all. Distinct from low confidence — there
	// is no transcript here to be unsure about.
	ClassRecogniserError = "recogniser_error"
	// ClassLowConfidence: a transcript exists and was reported below the
	// configured threshold. Requires both Confidence and Threshold.
	ClassLowConfidence = "low_confidence"
	// ClassConfidenceUnknown: no recognition confidence was reported for
	// this input. This is NOT a success and NOT a failure; it is the honest
	// name for "we cannot tell", and it is the state every dictation lands
	// in until an intake surface actually reports a confidence.
	ClassConfidenceUnknown = "confidence_unknown"
	// ClassNoMatch: the input parsed as a command but named no destination
	// matching a registered agent or known channel.
	ClassNoMatch = "no_match"
	// ClassRefused: the destination exists but delivery was refused — a
	// validation failure, a stale-target refusal, an unwritable store.
	ClassRefused = "refused"
	// ClassUnpicked: the input was queued and no listener picked it up.
	// Recorded only when something has actually waited long enough to say
	// so; a queue is not a failure the instant it is written.
	ClassUnpicked = "unpicked"
	// ClassSuperseded: a later input for the same destination replaced this
	// one before it was acted on.
	ClassSuperseded = "superseded"
	// ClassHeld: the input was held rather than routed, by policy.
	ClassHeld = "held"
)

// knownStages is the closed vocabulary of hops.
var knownStages = map[string]bool{
	StageReceived:    true,
	StageInterpreted: true,
	StageRouted:      true,
	StageQueued:      true,
	StageDelivered:   true,
	StageHeld:        true,
	StageSuperseded:  true,
}

// knownClasses is the closed vocabulary of outcomes.
var knownClasses = map[string]bool{
	ClassOK:                true,
	ClassRecogniserError:   true,
	ClassLowConfidence:     true,
	ClassConfidenceUnknown: true,
	ClassNoMatch:           true,
	ClassRefused:           true,
	ClassUnpicked:          true,
	ClassSuperseded:        true,
	ClassHeld:              true,
}

// Event is one recorded hop.
//
// The JSON field names are the wire shape a view reads; keep them stable.
// Seq is assigned by the log at write time (0 before then) and is the
// ordering key a replay follows.
type Event struct {
	Seq     uint64 `json:"seq"`
	Ts      string `json:"ts"`
	InputID string `json:"inputId"`
	Stage   string `json:"stage"`
	Class   string `json:"class"`
	Source  string `json:"source,omitempty"`

	// Channel is the destination the input was aimed at, when one was
	// chosen. Empty at StageReceived: an intake surface does not know the
	// destination yet, and guessing one would be a fabricated hop.
	Channel string `json:"channel,omitempty"`

	// Confidence and Threshold are the reported recognition confidence and
	// the threshold it was compared against. Both are *float64 so "absent"
	// (unknown) is distinguishable from "reported as 0".
	Confidence *float64 `json:"confidence,omitempty"`
	Threshold  *float64 `json:"threshold,omitempty"`

	// Reason is a short machine-readable token naming why a non-ok hop did
	// not succeed (`empty-transcript`, `no-agent-registered`, …). Required
	// for every class except ok. Never a message body, never an error
	// string that could carry one.
	Reason string `json:"reason,omitempty"`

	// Detail is bounded non-content context for a human (a channel count, a
	// source name). Never message text.
	Detail string `json:"detail,omitempty"`
}

// Validate reports whether an event may be stored. It is deliberately
// strict: a rejected event is counted and surfaced, so a gap in the view is
// never invisible.
func (e Event) Validate() error {
	if e.InputID == "" {
		return fmt.Errorf("inputId is required")
	}
	if !knownStages[e.Stage] {
		return fmt.Errorf("unknown stage %q", e.Stage)
	}
	if !knownClasses[e.Class] {
		return fmt.Errorf("unknown class %q", e.Class)
	}
	if e.Class != ClassOK && e.Reason == "" {
		return fmt.Errorf("class %q needs a reason: an unexplained failure is a defect, not a state", e.Class)
	}
	if err := checkUnit(e.Confidence, "confidence"); err != nil {
		return err
	}
	if err := checkUnit(e.Threshold, "threshold"); err != nil {
		return err
	}
	if e.Class == ClassLowConfidence {
		if e.Confidence == nil || e.Threshold == nil {
			return fmt.Errorf("class %q needs both confidence and threshold", ClassLowConfidence)
		}
	}
	return nil
}

func checkUnit(v *float64, name string) error {
	if v == nil {
		return nil
	}
	if *v < 0 || *v > 1 {
		return fmt.Errorf("%s %v is outside [0,1]", name, *v)
	}
	return nil
}

// inputIDSeq disambiguates two refusals minted in the same nanosecond.
var inputIDSeq atomic.Uint64

// NewInputID mints a ledger-local id for an input that never became a
// message — a request an intake surface refused before storing anything.
//
// Such a refusal has no message id to key on, and the alternative (skipping
// it) would make the most common failure on the seam invisible. The id is
// deliberately not exposed on any wire response: the public endpoint shapes
// are frozen, and a refusal is something the operator reads in the view
// rather than something it replays by id.
func NewInputID() string {
	return fmt.Sprintf("in-%d-%d", time.Now().UnixNano(), inputIDSeq.Add(1))
}
