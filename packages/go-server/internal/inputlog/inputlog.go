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

// Verdict is the threshold decision for one input's reported confidence. It
// exists so the rule lives in exactly one place: the view's rendering, the
// hold at the intake, and the tests all read the same decision rather than
// three paraphrases of it.
type Verdict struct {
	// Class is the inputlog class this confidence lands in: ClassOK,
	// ClassLowConfidence, or ClassConfidenceUnknown.
	Class string
	// Hold is true only when the input must not be routed.
	Hold bool
	// Reason names why, for a hold. Empty otherwise.
	Reason string
}

// Judge applies a configured minimum confidence to a reported confidence.
//
// Three outcomes, and the middle one is the honest one:
//
//   - reported and >= min: ClassOK.
//   - reported and <  min: Hold, ClassLowConfidence.
//   - NOT reported: ClassConfidenceUnknown, and never a hold.
//
// Unknown never holds on purpose. A threshold that refused an absent value
// would refuse every input from every surface that does not report a
// confidence — today, all of them — which is a policy invented on no
// evidence. "Not reported" is its own visible state instead, and the view
// prints it as one rather than as confidence.
func Judge(minConfidence, confidence *float64) Verdict {
	if confidence == nil || *confidence < 0 || *confidence > 1 {
		return Verdict{Class: ClassConfidenceUnknown}
	}
	if minConfidence == nil || *minConfidence < 0 || *minConfidence > 1 {
		return Verdict{Class: ClassOK}
	}
	if *confidence < *minConfidence {
		return Verdict{Class: ClassLowConfidence, Hold: true, Reason: ReasonBelowConfidence}
	}
	return Verdict{Class: ClassOK}
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
