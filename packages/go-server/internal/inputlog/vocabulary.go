// Stage, Class and Reason are the ledger's closed vocabulary: every hop an
// input makes is one Stage carrying one Class, and every non-ok Class carries
// a Reason token. The sets live here, apart from the record they describe, so
// that adding a state is one edit in one obvious place and an unclassified
// event stays impossible to store (see Event.Validate in inputlog.go).
//
// Nothing in this file carries message text: a Class and a Reason are tokens a
// view can render, not a copy of what the operator said.
package inputlog

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

// Reason tokens this package itself produces. A reason is a short
// machine-readable token, never a message body: it names why a hop did not
// succeed without carrying what the operator said.
const (
	// ReasonBelowConfidence is the hold reason: a transcript was reported
	// with a confidence below the configured threshold.
	ReasonBelowConfidence = "below-confidence-threshold"
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
