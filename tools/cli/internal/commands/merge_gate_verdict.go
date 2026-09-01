package commands

import (
	"fmt"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// finalizeVerdict turns the accumulated blockers into the gate's final
// exit-code decision. Class precedence, harshest first: code (3) > pending
// (5) > infra (6) > reviewer unavailable (4). Each arm only runs when no
// harsher class is present.
//
// Code first for the reason it always was: a failing test is still a
// failing test whatever else is also wrong, and no downgrade may ever
// launder it into somebody else's problem.
//
// Pending outranks both of the rest because a running check means the
// picture is still incomplete — asking the captain to choose
// merge-and-disclose while a review is mid-flight is a decision made on
// information that is about to arrive, and `gh run rerun` refuses a run
// that still has jobs in flight anyway. Wait, re-run, and the verdict
// resolves into a real 0/3/4/6.
//
// Infra outranks reviewer-unavailable because it is the one the caller can
// still act on alone: re-running the failed jobs is a bounded, mechanical
// step, where reviewer-unavailability is terminal until the captain picks.
func finalizeVerdict(v *MergeGateVerdict, s MergeGateSnapshot, rerunHint string) {
	switch {
	case len(v.Blockers) == 0:
		v.Ready, v.ExitCode = true, config.ExitOK
	case hasUnclassifiedOrCode(v.Blockers):
		v.ExitCode = ExitMergeBlocked
	case hasClass(v.Blockers, ClassPending):
		// Nothing here says anything is wrong with the code — the reviewer is
		// mid-sentence. Do not edit, do not decide, do not merge; re-run.
		v.Pending, v.ExitCode = true, ExitMergePending
		// The two pending shapes need opposite instructions, so name whichever
		// one is actually present rather than printing the review-in-flight
		// script over an unpushed head (robots-bn5d).
		if hasCode(v.Blockers, "head-not-pushed") {
			v.Notes = append(v.Notes,
				"Your local work is not at origin yet, so the verdict above is about an OLDER commit than the one you wrote — exit 5, not 3.",
				"Do NOT merge: merging lands origin's head and silently drops the local commits. Get them to origin first (let the pipeline run finish pushing, or push directly), then re-run `parlay merge-gate`.",
				"Expect the answer to CHANGE once the push lands — a new head restarts the review, so a 0 here can legitimately become 3, 4 or 5 afterwards. Bound the wait: if the push never arrives, that is a stuck pipeline, so signal `parlay status blocked` rather than polling forever.")
		}
		if hasPendingCode(v.Blockers, "check-pending", "stale-review", "no-review-evidence") {
			v.Notes = append(v.Notes,
				"Every review blocker above is the review still RUNNING, not a finding about this code — exit 5, not 3.",
				"Do NOT edit the branch to clear this: there is no defect to fix yet, and a new push restarts whatever review is in flight.",
				"Re-run `parlay merge-gate` after the check reports. Bound the wait — if it never finishes, that is reviewer unavailability, so signal `parlay status needs-decision` rather than polling forever (robots-8kkq).")
		}
	case hasClass(v.Blockers, ClassInfra):
		// The checks failed, but not at anything in this diff — they died in
		// GitHub before the repo's code ran, or were cancelled without
		// reporting. Do not send a mechanic hunting a defect in code that
		// never executed.
		v.Infra, v.ExitCode = true, ExitMergeInfra
		rerun := "gh run rerun <run-id> --failed"
		if rerunHint != "" {
			rerun = fmt.Sprintf("gh run rerun %s --failed", rerunHint)
		}
		if s.Repo != "" {
			rerun += " --repo " + s.Repo
		}
		v.Notes = append(v.Notes,
			"Every blocker above is a check that failed WITHOUT EVALUATING THIS DIFF — a GitHub-side error during job setup, or a job cancelled before it reported. Nothing here is a finding about your code, and no edit to the branch can clear it.",
			"Do NOT go hunting a defect: the failing jobs never ran this repository's code. A check that failed without running says as little about the diff as one that passed without running (robots-jap6).",
			fmt.Sprintf("Re-run the failed jobs — `%s` — then re-run `parlay merge-gate`.", rerun),
			"Bound it (robots-8kkq): if a re-run dies on the same infra signature, that is a GitHub incident, not your branch. Signal `parlay status needs-decision` with that reason instead of re-running forever.")
	default:
		// Nothing here is about the diff, and nothing on the branch will
		// change it. Say so, and say what the honest answers are, so the caller
		// has a terminating condition instead of a poll loop.
		v.NeedsDecision, v.ExitCode = true, ExitMergeNeedsDecision
		v.Notes = append(v.Notes,
			"Every blocker above is the reviewer being unavailable, not a finding about this code. Do NOT edit the branch: there is no finding to fix, and a new push restarts the review and re-consumes the limit that is blocking you.",
			"Waiting alone never clears this. CodeRabbit does not review this repository automatically at all — it told us why, on PR #122: \"This repository does not receive automatic reviews because it has fewer than 10 stars.\" So there is no reviewer who is going to come back on their own, and `park` is not a terminating strategy here; a gate re-run with no re-request returns this same answer forever.",
			fmt.Sprintf("Do this first, before treating it as a decision: post the re-request. `%s`. It works — verified on #122, which came back \"Action performed: Review finished\" in about a minute and immediately found a real goroutine/fd leak. Post it ONCE per head sha; a second one while the first is unanswered spends nothing.", requestReviewCmd(s.Repo, s.PR.Number)),
			"Get the branch to its FINAL head before you spend the re-request. If a `behind-base` blocker is listed above, merge or rebase first: a review is pinned to the sha it ran on, so updating the branch afterwards turns the review you just waited an hour for into `stale-review`, and the next one costs another window.",
			"If the reply is \"Review rate limited\", that is a quota, not a refusal: the free tier includes roughly one review per hour, and the reply states the wait (\"Next included review available in NN minutes\"). Re-request after that window rather than escalating. Sequence re-requests across PRs — firing them at three PRs at once spends the window on whichever lands first and the other two get nothing.",
			"Only once a re-request has been made AND its stated window has lapsed is this a real decision: signal `parlay status needs-decision` and let the captain choose merge-and-disclose (land it, stating plainly in the merge note that no review ran) or park. Do not escalate before spending the re-request — that is handing back a decision the gate could have resolved for the price of one comment.")
	}
}

// hasCode reports whether any blocker carries exactly this code.
func hasCode(bs []MergeBlocker, code string) bool {
	for _, b := range bs {
		if b.Code == code {
			return true
		}
	}
	return false
}

// hasPendingCode reports whether any pending-class blocker carries one of these
// codes. `stale-review`/`no-review-evidence` can also be reviewer-unavailable
// (a refused check), so the review-in-flight note must key on class, not code
// alone, or it prints "the review is still RUNNING" over a review that already
// answered no.
func hasPendingCode(bs []MergeBlocker, codes ...string) bool {
	for _, b := range bs {
		if b.Class != ClassPending {
			continue
		}
		for _, c := range codes {
			if b.Code == c {
				return true
			}
		}
	}
	return false
}

// hasClass reports whether any blocker carries exactly this class.
func hasClass(bs []MergeBlocker, class string) bool {
	for _, b := range bs {
		if b.Class == class {
			return true
		}
	}
	return false
}

// hasUnclassifiedOrCode reports whether any blocker is a hard block on the
// code. Anything not positively identified as one of the softer classes counts
// — an unrecognized or empty class keeps the harshest exit code, which is the
// conservative direction for a gate. A downgrade must be something the code
// deliberately decided, never something it forgot.
func hasUnclassifiedOrCode(bs []MergeBlocker) bool {
	for _, b := range bs {
		switch b.Class {
		case ClassPending, ClassReviewerUnavailable, ClassInfra:
		default:
			return true
		}
	}
	return false
}
