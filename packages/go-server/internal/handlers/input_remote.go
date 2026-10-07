// Input-seam recording for the dictation intake
// (POST /api/chat/remote-input/submit).
//
// This is the seam the operator's phone dictation actually lands on, and
// before this file it recorded nothing at all: a submission that was held,
// one whose transcript never arrived, and one that typed successfully left
// the same silence. The hops below are the whole answer to "did the
// recogniser mishear it, or did the phone never send it".
//
// Two rules keep it honest. First, reason tokens are short machine-readable
// names, never the backend's error string: a Talon or bead error could echo
// the text that was being typed, and the ledger must never become a second
// copy of the message log. The full error stays where it already was, on the
// submission's own status. Second, a status with no mapping is recorded as a
// refusal with its own reason rather than dropped — an unexplained failure
// is a defect in this tooling, not a quiet night.
package handlers

import (
	"parlay/go-server/internal/inputlog"
	"parlay/go-server/internal/remoteinput"
	"parlay/go-server/internal/store"
)

// inputSourceRemoteInput is the door a remote-input submission came through.
const inputSourceRemoteInput = "remote-input"

// Reason tokens the dictation intake records. Each names a distinct failure
// the operator previously could not see; none carries message text.
const (
	reasonMissingDevice    = "missing-device"
	reasonEmptyTranscript  = "empty-transcript"
	reasonUnknownMode      = "unknown-mode"
	reasonInvalidStore     = "invalid-store"
	reasonBeadTextTooLong  = "bead-text-too-long"
	reasonNoTarget         = "no-target"
	reasonTargetNotMatched = "target-not-matched"
	reasonInjectFailed     = "inject-failed"
	reasonBeadFailed       = "bead-failed"
	reasonUnknownOutcome   = "unknown-outcome"
)

// remoteInputSeam writes the dictation intake's hops into the ledger. A nil
// store (handler tests that only exercise the wire shape) turns every method
// into a no-op, so recording can never be the reason a request fails.
type remoteInputSeam struct{ st *store.Store }

func (s remoteInputSeam) log() *inputlog.Log {
	if s.st == nil {
		return nil
	}
	return s.st.Input
}

// minConfidence is the threshold in force, or nil when disabled.
func (s remoteInputSeam) minConfidence() *float64 {
	if l := s.log(); l != nil {
		return l.MinConfidence()
	}
	return nil
}

// refused records input an intake surface declined before storing anything.
func (s remoteInputSeam) refused(inputID, reason string) {
	l := s.log()
	if l == nil {
		return
	}
	l.Record(inputlog.Event{
		InputID: inputID, Stage: inputlog.StageInterpreted, Class: inputlog.ClassRefused,
		Source: inputSourceRemoteInput, Reason: reason,
	})
}

// recogniserError records input that arrived and became nothing: the
// dictation surface sent an empty transcript. It is deliberately not a
// refusal — nothing was declined — and not silence, which is what it used to
// look like when the only trace was a 400 the phone logged and dropped.
func (s remoteInputSeam) recogniserError(inputID string) {
	l := s.log()
	if l == nil {
		return
	}
	l.Record(inputlog.Event{
		InputID: inputID, Stage: inputlog.StageInterpreted, Class: inputlog.ClassRecogniserError,
		Source: inputSourceRemoteInput, Reason: reasonEmptyTranscript,
	})
}

// intake records the hops every submission has once it has an id: received,
// then interpreted when a recognition confidence was actually reported. It
// is called from the accept hook and, for a held submission, directly — the
// hold never reaches the service's queue, so nothing else would record it.
//
// Invariant: a submission whose verdict is a hold never reaches the accept
// hook, because the handler applies the threshold before it queues anything
// (see acceptRemoteInput). The interpreted hop here therefore describes a
// measurement, never a hold; the held hop is recorded only by held().
func (s remoteInputSeam) intake(sub remoteinput.Submission) {
	l := s.log()
	if l == nil {
		return
	}
	l.Record(inputlog.Event{
		InputID: sub.ID, Stage: inputlog.StageReceived, Class: inputlog.ClassOK,
		Source: inputSourceRemoteInput, Detail: "mode=" + remoteinput.NormalizeMode(sub.Mode),
	})
	if sub.Confidence == nil {
		return
	}
	v := inputlog.Judge(s.minConfidence(), sub.Confidence)
	l.Record(inputlog.Event{
		InputID: sub.ID, Stage: inputlog.StageInterpreted, Class: v.Class,
		Source: inputSourceRemoteInput, Reason: v.Reason,
		Confidence: sub.Confidence, Threshold: s.minConfidence(),
	})
}

// held records the policy hold: the threshold, the confidence that missed
// it, and a state that says the input was stopped on purpose.
func (s remoteInputSeam) held(sub remoteinput.Submission) {
	l := s.log()
	if l == nil {
		return
	}
	l.Record(inputlog.Event{
		InputID: sub.ID, Stage: inputlog.StageHeld, Class: inputlog.ClassHeld,
		Source: inputSourceRemoteInput, Reason: inputlog.ReasonBelowConfidence,
		Confidence: sub.Confidence, Threshold: s.minConfidence(),
	})
}

// settled maps a terminal submission outcome onto the seam's vocabulary.
// The mapping is by status, not by parsing the backend's error text.
func (s remoteInputSeam) settled(o remoteinput.Outcome) {
	l := s.log()
	if l == nil {
		return
	}
	detail := "outcome=" + o.Status
	if o.Mode != "" {
		detail += " mode=" + o.Mode
	}
	switch o.Status {
	case remoteinput.StatusHeld:
		// The hold is recorded where it is decided, with the confidence and
		// threshold that caused it; the outcome carries neither.
		return
	case remoteinput.StatusInjected, remoteinput.StatusDryRunPassed, remoteinput.StatusBeadCreated:
		l.Record(inputlog.Event{
			InputID: o.ID, Stage: inputlog.StageDelivered, Class: inputlog.ClassOK,
			Source: inputSourceRemoteInput, Detail: detail,
		})
	case remoteinput.StatusFocusFailed:
		l.Record(inputlog.Event{
			InputID: o.ID, Stage: inputlog.StageRouted, Class: inputlog.ClassNoMatch,
			Source: inputSourceRemoteInput, Reason: reasonTargetNotMatched, Detail: detail,
		})
	case remoteinput.StatusInjectFailed:
		l.Record(inputlog.Event{
			InputID: o.ID, Stage: inputlog.StageDelivered, Class: inputlog.ClassRefused,
			Source: inputSourceRemoteInput, Reason: reasonInjectFailed, Detail: detail,
		})
	case remoteinput.StatusBeadFailed:
		l.Record(inputlog.Event{
			InputID: o.ID, Stage: inputlog.StageRouted, Class: inputlog.ClassRefused,
			Source: inputSourceRemoteInput, Reason: reasonBeadFailed, Detail: detail,
		})
	default:
		l.Record(inputlog.Event{
			InputID: o.ID, Stage: inputlog.StageRouted, Class: inputlog.ClassRefused,
			Source: inputSourceRemoteInput, Reason: reasonUnknownOutcome, Detail: detail,
		})
	}
}
