package commands

import "fmt"

// ExitMergeBlocked is distinct from 1 (gh/runtime error) so a scripted caller
// can tell "the gate answered and said no" from "the gate could not answer".
// Both are non-zero — a gate must fail closed either way.
const ExitMergeBlocked = 3

// ExitMergeNeedsDecision is the gate's answer when every blocker it found is
// about the REVIEWER being unavailable rather than about the code. Still
// non-zero, so the naive "non-zero = do not merge" caller is unchanged and
// still fails closed; a caller that reads the code knows the difference
// between "wait, this will resolve" and "this needs a human to choose".
const ExitMergeNeedsDecision = 4

// ExitMergePending is the gate's answer when every blocker it found is the
// review still being IN FLIGHT. Non-zero, so the naive "non-zero = do not
// merge" caller is unchanged and still fails closed — but unlike 3 it is not
// a statement about the code, and unlike 4 it is not terminal. The only
// correct response is to re-run the gate later (robots-rwf8).
const ExitMergePending = 5

// ExitMergeInfra is the gate's answer when every blocker it found is a check
// that failed WITHOUT EVALUATING THE DIFF — a GitHub-side error during action
// setup, or a job cancelled before it reported. Non-zero, so the naive
// "non-zero = do not merge" caller is unchanged and still fails closed. Unlike
// 3 it says nothing about the code, and unlike 4 the caller can act on it
// alone: re-run the failed jobs, then re-run the gate (robots-6mw2).
const ExitMergeInfra = 6

// Blocker classes. ClassCode is the default and means the finding is about
// this PR — fix it here. ClassReviewerUnavailable means the PR may be
// perfectly fine and the reviewing service simply did not participate; no
// amount of work on the branch changes it. ClassPending means the review has
// not finished yet: nothing is known to be wrong, no action on the branch
// helps, and the answer will change on its own. ClassInfra means a check
// failed before it ever looked at the diff, so it is evidence about GitHub,
// not about this code — a re-run, not an edit.
const (
	ClassCode                = "code"
	ClassReviewerUnavailable = "reviewer-unavailable"
	ClassPending             = "pending"
	ClassInfra               = "infra"
)

// MergeBlocker is one reason the PR must not be merged. Code is a stable
// machine-readable slug; Detail is the human sentence; Class says whether
// acting on this blocker is even possible from the PR.
type MergeBlocker struct {
	Code   string `json:"code"`
	Class  string `json:"class"`
	Detail string `json:"detail"`
}

// MergeGateVerdict is the gate's answer.
type MergeGateVerdict struct {
	Ready  bool `json:"ready"`
	Merged bool `json:"merged"`
	// NeedsDecision is true when there ARE blockers but every one of them is
	// reviewer-unavailability. The PR is not ready and must not be
	// auto-merged, but nothing here is fixable on the branch — the captain
	// has to choose merge-and-disclose or park.
	NeedsDecision bool `json:"needsDecision"`
	// Pending is true when there ARE blockers but every one of them is the
	// review still running. Nothing is known to be wrong with the code and
	// nothing needs deciding — the caller re-runs the gate later.
	Pending bool `json:"pending"`
	// BehindKnown mirrors MergeGateSnapshot.BehindKnown: false when the
	// base-comparison call failed, so FormatMergeGate can qualify the
	// ready summary rather than asserting "green against the current base".
	BehindKnown bool `json:"behindKnown"`
	// Infra is true when there ARE blockers but every one of them is a check
	// that failed without evaluating the diff. Nothing is known to be wrong
	// with the code; the caller re-runs the failed jobs (robots-6mw2).
	Infra bool `json:"infra"`
	// OriginLagsLive is true when the local base branch has commits origin's
	// copy does not, so merging this PR is not the same act as deploying it
	// (robots-oex0). It is NOT a blocker and never changes the exit code:
	// nothing about the drift makes the PR unsafe to merge. What it makes
	// unsafe is the sentence "merged, therefore fixed", so it is surfaced in
	// the header instead — a mechanic who reads only the first line is exactly
	// the one who needs it.
	OriginLagsLive bool           `json:"originLagsLive"`
	Blockers       []MergeBlocker `json:"blockers"`
	Notes          []string       `json:"notes"`
	ExitCode       int            `json:"exitCode"`
}

func block(v *MergeGateVerdict, code, format string, a ...any) {
	blockAs(v, code, ClassCode, format, a...)
}

// blockAs records a blocker with an explicit class. Everything that is not
// positively identified as reviewer-unavailability stays ClassCode, so an
// unrecognized failure keeps the harsher exit code — the conservative
// direction for a gate.
func blockAs(v *MergeGateVerdict, code, class, format string, a ...any) {
	v.Blockers = append(v.Blockers, MergeBlocker{Code: code, Class: class, Detail: fmt.Sprintf(format, a...)})
}
