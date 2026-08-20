// parlay merge-gate — the truthful "is this PR actually safe to merge?" verb
// (robots-jap6).
//
// The defect this exists to fix: a green status check is NOT evidence that
// anything reviewed the code. CodeRabbit — the only check on this repo, since
// there are no .github/workflows at all — reports a check CONCLUSION of
// `pass` even when it never ran, because the account hit its PR-review rate
// limit. `gh pr checks 45` prints:
//
//	CodeRabbit  pass  0  Review rate limited
//
// The word "pass" is the lie; the free-text description is the only truthful
// field, and `gh pr view` compounds it with mergeStateStatus=CLEAN,
// mergeable=MERGEABLE, reviews=0. Any agent following the standing mechanic
// guardrail "merge when all required checks are green" therefore auto-merges
// completely unreviewed code. PRs #43 and #46 both landed that way.
//
// This is the second known way that check is untruthful — the captain
// previously found it reports success regardless of how many findings the
// review posted. So this verb deliberately does NOT trust the check
// conclusion as the merge signal at all. It asserts, in order:
//
//  1. the PR is open and not conflicting,
//  2. there is at least one check, and no check is failing or still pending,
//  3. no check is a VACUOUS pass — conclusion green but description
//     admitting it did not run (the rate-limit case above),
//  4. a review actually HAPPENED: a human review, or a CodeRabbit comment
//     carrying a real-review marker rather than the rate-limit template,
//  5. that review covered the CURRENT head sha, not an older push,
//  6. no review thread is left unresolved (the findings-count lie).
//
// Exit codes are deliberately fail-closed in every direction: 0 only when
// the PR is genuinely ready (or already merged), 3 when a real blocker in the
// code is found, 5 when the reviewer simply has not finished yet, 4 when the
// only thing missing is a reviewer that is unavailable, 1 when gh/network
// could not answer, 2 on usage. A caller that just branches on non-zero
// refuses the merge in all five failure modes, which is the correct default
// for a gate.
//
// 4 exists because 3 alone gave the fleet no bounded answer (robots-8kkq).
// "CodeRabbit is rate limited" and "a test is failing" are both non-zero, but
// they call for opposite behavior: the second is fixed by working on the PR,
// while the first cannot be fixed from the PR at all and has already, in
// practice, outlasted the stated window by hours — `@coderabbitai review`
// recovered one PR once and then stayed limited across three further attempts
// over ~40 minutes. A mechanic told only "blocked" polls forever. Splitting
// the exit code lets the caller stop and hand the captain the two honest
// options — merge-and-disclose, or park — instead of burning the night on a
// wait with no terminating condition.
//
// A refusal counts wherever it is written down (robots-eowy). CodeRabbit
// edits its ONE comment in place, so a PR whose first push got a real review
// keeps that walkthrough body forever; when a later push is refused, the only
// place the refusal appears is the check DESCRIPTION. Classifying off the
// comment alone made that shape exit 3 — `vacuous-pass` + `stale-review` on
// trillium/no-mistakes#13 — which is the worst possible answer: it sends a
// mechanic hunting a defect in code no reviewer ever objected to, and every
// edit it makes pushes a new head, restarting the review and re-consuming the
// limit that is blocking it. A vacuous check therefore reclassifies
// `stale-review` and `no-review-evidence` exactly as a rate-limit comment
// does. `no-review-evidence` keeping the harsher code was only ever justified
// by the gate not knowing WHY nothing reviewed the PR; a check that states the
// reason is that knowledge.
//
// 5 exists for the same reason in the opposite direction (robots-rwf8). A
// check that is STILL RUNNING was landing in 3 — the code the mechanic
// contract documents as "blocked on the CODE, fix it on the branch" — even
// though nothing was wrong with the diff and the reviewer had simply not
// finished. Observed on trillium/no-mistakes#11: `check-pending` plus
// `no-review-evidence`, exit 3, and minutes later the SAME unchanged PR
// exited 0. An agent obeying the documented contract goes editing a branch
// that has no defect. "Not yet" is neither "the code is wrong" (3) nor "the
// reviewer will never come" (4); it is its own answer, and the only one of
// the three that is genuinely transient. Re-run the gate; do not edit, do not
// escalate, do not merge.
//
// Separately from all of that, the gate reports when merging is not the same
// act as DEPLOYING (robots-oex0). The mechanic contract proves a fix landed by
// showing origin/main contains the commit — which assumes origin/main is the
// artifact that runs. In `pai-hooks` it is not: `~/.claude/hooks` symlinks
// into the checkout, so local main is live and origin was 20 commits behind
// it. Both halves of the proof failed at once — a merged commit satisfied
// "FIXED" without going live, and a commit that WAS live was not on
// origin/main at all. The gate cannot know what a repo deploys, but it can see
// the drift that makes "merged" and "live" come apart, so it says so in the
// header and leaves the exit code alone: the PR is still safe to merge, it is
// the conclusion drawn afterwards that is not safe.

package commands

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

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

// Blocker classes. ClassCode is the default and means the finding is about
// this PR — fix it here. ClassReviewerUnavailable means the PR may be
// perfectly fine and the reviewing service simply did not participate; no
// amount of work on the branch changes it. ClassPending means the review has
// not finished yet: nothing is known to be wrong, no action on the branch
// helps, and the answer will change on its own.
const (
	ClassCode                = "code"
	ClassReviewerUnavailable = "reviewer-unavailable"
	ClassPending             = "pending"
)

// vacuousCheckDesc matches a status-check description that ADMITS the check
// did no work, even though its conclusion is green. The description is the
// only field CodeRabbit fills in truthfully when it is rate limited, so it is
// the field this gate reads.
var vacuousCheckDesc = regexp.MustCompile(`(?i)rate[ -]?limit|limit reached|review skipped|skipping review|not reviewed|never ran|review unavailable`)

// coderabbitRateLimited matches CodeRabbit's rate-limit comment. The HTML
// marker is machine-generated and stable; the human heading is the fallback
// in case the marker is ever dropped.
var coderabbitRateLimited = regexp.MustCompile(`(?i)rate limited by coderabbit\.ai|review limit reached`)

// coderabbitReviewed matches a CodeRabbit comment that represents an actual
// completed review. `walkthrough_start` is the machine marker wrapping the
// generated walkthrough; "actionable comments posted" heads a real findings
// list. Both only ever appear once a review truly ran.
//
// "Files selected for processing" is deliberately NOT in this set, even
// though it reads like review evidence: the rate-limit template embeds a
// "Review details" section listing the files it WOULD have reviewed, so
// matching it classifies a refusal as a review. Caught on this very fix's
// own PR (#47) — see reviewEvidence for the ordering that makes it moot
// anyway.
var coderabbitReviewed = regexp.MustCompile(`(?i)<!-- walkthrough_start -->|actionable comments posted`)

// sha40 pulls full commit sha's out of a CodeRabbit review body, which states
// the exact range it reviewed ("...changed from the base of the PR and
// between <base> and <head>"). Comparing the head sha against that range is
// how a review of an older push is caught, since GitHub's comment timestamps
// are useless here — CodeRabbit EDITS one comment in place, so createdAt
// stays pinned to the first push forever.
var sha40 = regexp.MustCompile(`\b[0-9a-f]{40}\b`)

// ghAuthor is a PR/comment author.
type ghAuthor struct {
	Login string `json:"login"`
}

// ghComment is one issue comment on the PR.
type ghComment struct {
	Author ghAuthor `json:"author"`
	Body   string   `json:"body"`
}

// ghReview is one GitHub PR review (distinct from an issue comment).
type ghReview struct {
	Author ghAuthor `json:"author"`
	Body   string   `json:"body"`
	State  string   `json:"state"`
}

// ghCheck is one row of `gh pr checks --json name,state,bucket,description`.
// Bucket is gh's normalization of the many per-provider states into
// pass/fail/pending/skipping/cancel.
type ghCheck struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Bucket      string `json:"bucket"`
	Description string `json:"description"`
}

// ghPRView is the subset of `gh pr view --json …` this gate reads.
type ghPRView struct {
	Number           int         `json:"number"`
	URL              string      `json:"url"`
	State            string      `json:"state"`
	Mergeable        string      `json:"mergeable"`
	MergeStateStatus string      `json:"mergeStateStatus"`
	HeadRefOid       string      `json:"headRefOid"`
	BaseRefName      string      `json:"baseRefName"`
	Author           ghAuthor    `json:"author"`
	Reviews          []ghReview  `json:"reviews"`
	Comments         []ghComment `json:"comments"`
}

// LiveBranchState compares the LOCAL base branch against `origin/<base>` —
// the difference between "this PR merged" and "this fix is live" (robots-oex0).
//
// The mechanic contract proves a fix LANDED with `git branch -r --contains
// <sha>` listing origin/main plus `gh pr view` reporting MERGED. That proof
// silently assumes origin/main IS the deployed artifact. In a repo whose
// working tree is itself the deployment target it is not: `~/.claude/hooks`
// is a symlink into the `pai-hooks` checkout, so the hooks that actually run
// are whatever local `main` says, and origin is a lagging mirror. Measured on
// that repo, origin/main was 20 commits behind local main — so both halves of
// the proof were wrong at once. A commit merged to origin/main satisfied
// "FIXED" without ever going live, and a commit that WAS live (cdaf08f) was
// not on origin/main at all and could never satisfy it.
//
// The gate cannot know what a given repo deploys, and must not guess. What it
// CAN observe is the drift itself, which is exactly the condition under which
// "merged" and "live" are allowed to disagree — so it reports the drift and
// lets the caller stop equating the two.
type LiveBranchState struct {
	// Known is false when the comparison could not be made at all (not in a
	// git work tree, no local branch of that name, no origin remote-tracking
	// ref). The gate then says nothing rather than implying agreement.
	Known bool `json:"known"`
	// Branch is the PR's base branch, e.g. "main".
	Branch string `json:"branch"`
	// Ahead counts commits on the LOCAL base branch that origin's copy does
	// not have — i.e. how far origin lags whatever this checkout runs.
	Ahead int `json:"ahead"`
	// Behind counts commits on origin's copy that the local branch does not
	// have. Normal and harmless on its own (a `git pull` fixes it); reported
	// only because a two-sided divergence needs a merge, not a fast-forward.
	Behind int `json:"behind"`
}

// MergeGateSnapshot is everything the gate needs about a PR, already fetched.
// Keeping it a plain struct is what lets ComputeMergeGate stay pure and
// unit-testable without a network or a gh binary.
type MergeGateSnapshot struct {
	PR ghPRView
	// Repo is the "owner/name" every gh call in this run was pinned to, and
	// RepoSource says how it was chosen. Both are reported, because a gate
	// that answers about the wrong repository is worse than no gate.
	Repo       string
	RepoSource string
	// Checks is empty when the PR has no checks reported at all — which is
	// itself a blocker, not a pass.
	Checks []ghCheck
	// Live is the local origin-vs-deployed-branch comparison. Zero value
	// (Known=false) means it could not be measured, and the gate stays quiet.
	Live LiveBranchState
	// UnresolvedThreads counts review threads still marked unresolved.
	UnresolvedThreads int
	// ThreadsKnown is false when the review-thread query could not be run;
	// the gate then reports the gap instead of silently claiming zero.
	ThreadsKnown bool
}

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

// ComputeMergeGate is the whole decision, as a pure function of a snapshot.
func ComputeMergeGate(s MergeGateSnapshot) MergeGateVerdict {
	v := MergeGateVerdict{Blockers: []MergeBlocker{}, Notes: []string{}}

	// Which repository this answer is about comes FIRST, above even the
	// merged short-circuit — the whole robots-g4qz defect was a verdict that
	// read perfectly while describing a different repository's PR.
	if s.Repo != "" {
		src := s.RepoSource
		if src == "" {
			src = "unspecified"
		}
		v.Notes = append(v.Notes, fmt.Sprintf("repo: %s (from %s)", s.Repo, src))
	}
	if got, ok := repoFromPRURL(s.PR.URL); ok && s.Repo != "" && !strings.EqualFold(got, s.Repo) {
		v.Notes = append(v.Notes, fmt.Sprintf(
			"WARNING: asked about %s but GitHub answered for %s — if that is not a repository rename, this verdict is about the wrong PR.",
			s.Repo, got))
	}

	// Whether "merged" even means "live" comes before the MERGED
	// short-circuit, because an already-merged PR in a lagging repo is the
	// exact case the mechanic misreads as done (robots-oex0).
	noteOriginLagsLive(&v, s)

	switch strings.ToUpper(s.PR.State) {
	case "MERGED":
		// Already landed. Nothing left to gate — say so plainly rather than
		// running checks that no longer mean anything.
		v.Ready, v.Merged, v.ExitCode = true, true, config.ExitOK
		v.Notes = append(v.Notes, "PR is already MERGED — nothing left to gate.")
		return v
	case "CLOSED":
		block(&v, "pr-closed", "PR is CLOSED without being merged.")
		v.ExitCode = ExitMergeBlocked
		return v
	}

	if strings.EqualFold(s.PR.Mergeable, "CONFLICTING") {
		block(&v, "conflicting", "PR conflicts with the base branch (mergeable=CONFLICTING).")
	}

	// --- checks -------------------------------------------------------
	if len(s.Checks) == 0 {
		block(&v, "no-checks", "PR has no status checks at all — nothing gated this code.")
	}
	checkPending := false
	checkVacuous := false
	for _, c := range s.Checks {
		name := c.Name
		if name == "" {
			name = "(unnamed check)"
		}
		switch strings.ToLower(c.Bucket) {
		case "fail", "cancel":
			block(&v, "check-failed", "check %q is %s (%s).", name, c.Bucket, describeOrState(c))
		case "pending":
			// Classed pending, not code (robots-rwf8): a check that has not
			// finished has said NOTHING about the diff yet. Editing the branch
			// to "fix" it is editing code no one has objected to, and it also
			// invalidates whatever review was in flight.
			checkPending = true
			blockAs(&v, "check-pending", ClassPending,
				"check %q has not finished (%s).", name, describeOrState(c))
		default:
			// Green — but only if the check's own description does not admit
			// it never ran. This is the robots-jap6 defect: bucket=pass with
			// description="Review rate limited".
			if vacuousCheckDesc.MatchString(c.Description) {
				// Classed reviewer-unavailable, not code: the check admitting
				// it did no work says nothing bad about the diff. It is still
				// a blocker — absence of evidence is not evidence — but it is
				// not something the branch can be edited into passing.
				//
				// This is ALSO a live refusal of the current head, exactly like
				// a rate-limit comment body, and the rest of the gate treats it
				// as one (robots-eowy). It is often the ONLY place the refusal
				// is visible: CodeRabbit edits its one comment in place, so a
				// PR whose earlier push got a real review keeps that walkthrough
				// body forever while the check description flips to "Review rate
				// limited" for the new head.
				checkVacuous = true
				blockAs(&v, "vacuous-pass", ClassReviewerUnavailable,
					"check %q reports %s but its description says it did not run: %q. A green conclusion here is not evidence of anything.",
					name, c.Bucket, c.Description)
			}
		}
	}

	// --- review evidence ----------------------------------------------
	humanReviewer := ""
	for _, r := range s.PR.Reviews {
		if strings.EqualFold(r.Author.Login, s.PR.Author.Login) {
			continue // self-review is not review
		}
		switch strings.ToUpper(r.State) {
		case "APPROVED", "CHANGES_REQUESTED", "COMMENTED":
			humanReviewer = r.Author.Login
		}
	}

	// Order matters: the rate-limit check runs FIRST, and a body carrying that
	// marker is never counted as review evidence no matter what else it says.
	// CodeRabbit's refusal template is not a bare error — it embeds a "Review
	// details" section enumerating the files and the exact base..head range it
	// WOULD have processed, which reads exactly like a completed review to any
	// content match. Scanning for review markers first lets a refusal
	// masquerade as the review it explicitly declined to do.
	reviewedBodies := []string{}
	rateLimited := false
	for _, body := range botBodies(s.PR) {
		if coderabbitRateLimited.MatchString(body) {
			rateLimited = true
			continue
		}
		if coderabbitReviewed.MatchString(body) {
			reviewedBodies = append(reviewedBodies, body)
		}
	}

	// A refusal is a refusal wherever it is written down (robots-eowy). The
	// rate-limit COMMENT and a vacuous check DESCRIPTION are the same fact —
	// "the reviewer declined to look at this head" — and only one of the two
	// is present in the common case, because CodeRabbit edits its single
	// comment in place: a PR whose first push got a real review still shows
	// that walkthrough body after a later push is refused, and the refusal
	// exists only in the check description. Reading just the comment made
	// trillium/no-mistakes#13 exit 3 (`vacuous-pass` + `stale-review`), which
	// tells a mechanic to go find a defect in code no reviewer ever objected
	// to — and every edit it makes pushes a new head, restarting the review
	// and re-consuming the very limit that is blocking it.
	reviewerRefused := rateLimited || checkVacuous

	switch {
	case len(reviewedBodies) > 0:
		if s.PR.HeadRefOid != "" && !bodiesCoverHead(reviewedBodies, s.PR.HeadRefOid) {
			// A stale review is normally a code-class blocker: push again and
			// the reviewer catches up. But when a rate-limit template sits on
			// the PR alongside the old review, the re-review is exactly what
			// is being refused — that is the trillium/no-mistakes#7 shape,
			// where one `@coderabbitai review` recovered the first push and
			// the follow-up commit then never got reviewed at all.
			//
			// And it is neither of those while a check is still running: the
			// re-review of the new head is in flight, so this is "not yet",
			// not "fix it" (robots-rwf8). Rate limit still wins — a live
			// refusal outranks an unfinished check.
			class := ClassCode
			switch {
			case reviewerRefused:
				class = ClassReviewerUnavailable
			case checkPending:
				class = ClassPending
			}
			blockAs(&v, "stale-review", class,
				"the automated review covered an earlier commit, not the current head %s — the code that would merge is unreviewed.",
				shortSHA(s.PR.HeadRefOid))
		}
	case rateLimited:
		blockAs(&v, "review-rate-limited", ClassReviewerUnavailable,
			"CodeRabbit posted its rate-limit template and never reviewed this PR. Re-request after the window, or enable usage-based reviews.")
	case humanReviewer != "":
		v.Notes = append(v.Notes,
			fmt.Sprintf("No automated review found, but %s reviewed this PR — treating that as the review of record.", humanReviewer))
	default:
		// "Nothing reviewed this PR" normally stays code-class: the gate
		// cannot tell WHY nothing reviewed it, and unexplained gets the
		// harsher code. But a check that is still running IS the explanation
		// — the review is running right now and has not posted yet
		// (robots-rwf8). This pairing, `check-pending` + `no-review-evidence`,
		// is the exact shape that exited 3 on trillium/no-mistakes#11 and then
		// exited 0 minutes later, unchanged. Downgrading to pending never
		// reaches 0, so the gate still fails closed; it only stops telling the
		// mechanic to go edit a branch nobody has objected to.
		//
		// A vacuous check is the other explanation, and it is an explicit one:
		// the check itself says it did not run (robots-eowy). "Unexplained"
		// was always the whole justification for keeping this code-class, so
		// once the reviewer has stated the reason, the reason is what governs.
		// Refusal outranks pending for the same reason it does above — that
		// reviewer has already answered, and the answer was no.
		class := ClassCode
		switch {
		case reviewerRefused:
			class = ClassReviewerUnavailable
		case checkPending:
			class = ClassPending
		}
		blockAs(&v, "no-review-evidence", class,
			"nothing reviewed this PR: no human review, and no automated-review comment (only a check conclusion, which is not evidence).")
	}

	// --- open findings -------------------------------------------------
	if !s.ThreadsKnown {
		v.Notes = append(v.Notes, "Could not read review threads — unresolved findings are UNKNOWN, not zero.")
	} else if s.UnresolvedThreads > 0 {
		block(&v, "unresolved-threads",
			"%d review thread(s) are still unresolved. The check conclusion stays green regardless of finding count, so it will not tell you this.",
			s.UnresolvedThreads)
	}

	// Class precedence, harshest first: code (3) > pending (5) > reviewer
	// unavailable (4). Each arm only runs when no harsher class is present.
	//
	// Code first for the reason it always was: a failing test is still a
	// failing test whatever else is also wrong, and no downgrade may ever
	// launder it into somebody else's problem.
	//
	// Pending outranks reviewer-unavailable because a running check means the
	// picture is still incomplete — asking the captain to choose
	// merge-and-disclose while a review is mid-flight is a decision made on
	// information that is about to arrive. Wait, re-run, and the verdict
	// resolves into a real 0/3/4.
	switch {
	case len(v.Blockers) == 0:
		v.Ready, v.ExitCode = true, config.ExitOK
	case hasUnclassifiedOrCode(v.Blockers):
		v.ExitCode = ExitMergeBlocked
	case hasClass(v.Blockers, ClassPending):
		// Nothing here says anything is wrong with the code — the reviewer is
		// mid-sentence. Do not edit, do not decide, do not merge; re-run.
		v.Pending, v.ExitCode = true, ExitMergePending
		v.Notes = append(v.Notes,
			"Every blocker above is the review still RUNNING, not a finding about this code — exit 5, not 3.",
			"Do NOT edit the branch to clear this: there is no defect to fix yet, and a new push restarts whatever review is in flight.",
			"Re-run `parlay merge-gate` after the check reports. Bound the wait — if it never finishes, that is reviewer unavailability, so signal `parlay status needs-decision` rather than polling forever (robots-8kkq).")
	default:
		// Nothing here is about the diff, and nothing on the branch will
		// change it. Say so, and say what the two honest answers are, so the
		// caller has a terminating condition instead of a poll loop.
		v.NeedsDecision, v.ExitCode = true, ExitMergeNeedsDecision
		v.Notes = append(v.Notes,
			"Every blocker above is the reviewer being unavailable, not a finding about this code. Do NOT edit the branch: there is no finding to fix, and a new push restarts the review and re-consumes the limit that is blocking you.",
			"Do NOT wait on this unbounded, and do not expect waiting to work at all: CodeRabbit does not re-review on its own when the window lapses — it only reviews on a new push or an explicit `@coderabbitai review` comment, so a gate re-run alone will return this same answer forever.",
			"Signal `parlay status needs-decision` and let the captain pick one of three: re-request (post `@coderabbitai review` on the PR once — the only action that can change this answer, and it has stayed limited across repeated attempts before), merge-and-disclose (land it, and state plainly in the merge/close note that no review ran), or park (leave it open until the reviewer returns). Do not pick for them.")
	}
	return v
}

// noteOriginLagsLive records the merged-is-not-live warning when the local
// base branch is ahead of origin's copy (robots-oex0).
//
// Deliberately notes, not blockers. The drift says nothing bad about the diff
// and nothing on the branch can fix it, so making it exit non-zero would
// refuse merges that are perfectly fine — and a gate that cries wolf on every
// run of a repo like pai-hooks gets ignored on the run that matters. What the
// drift breaks is the INFERENCE the mechanic draws afterwards, so the answer
// is to say plainly that the inference is unavailable here.
func noteOriginLagsLive(v *MergeGateVerdict, s MergeGateSnapshot) {
	l := s.Live
	if !l.Known || l.Ahead <= 0 {
		return
	}
	v.OriginLagsLive = true
	v.Notes = append(v.Notes, fmt.Sprintf(
		"ORIGIN LAGS LIVE: local %s is %d commit(s) ahead of origin/%s, so origin/%s is a lagging mirror of the branch this checkout actually runs. Merging lands the commit on origin/%s — it does NOT put it in the deployed tree. `git branch -r --contains <sha>` will list origin/%s and still be wrong about liveness (robots-oex0).",
		l.Branch, l.Ahead, l.Branch, l.Branch, l.Branch, l.Branch))
	reconcile := fmt.Sprintf("git checkout %s && git pull --ff-only origin %s", l.Branch, l.Branch)
	if l.Behind > 0 {
		// Two-sided divergence: a fast-forward is impossible, so do not
		// suggest one — that is the shape that leaves a mechanic stuck.
		reconcile = fmt.Sprintf("git checkout %s && git merge origin/%s", l.Branch, l.Branch)
	}
	v.Notes = append(v.Notes, fmt.Sprintf(
		"Before claiming FIXED, make the DEPLOYED branch contain the commit and stop the two diverging: after this lands, `%s`, then push local %s to origin. If you cannot, say plainly that the fix is merged but not live — do not report it as done.",
		reconcile, l.Branch))
	if strings.EqualFold(s.PR.Mergeable, "CONFLICTING") {
		// Same root cause, different symptom: a branch cut from the local
		// base drags every unpushed commit into the PR, which GitHub reports
		// as a conflict against a base that never saw them.
		v.Notes = append(v.Notes, fmt.Sprintf(
			"The CONFLICTING status above is most likely this same drift: a branch cut from local %s carries those %d unpushed commit(s) into the PR. `git rebase --onto origin/%s %s <branch>` gives the real diff.",
			l.Branch, l.Ahead, l.Branch, l.Branch))
	}
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
// code. Anything not positively identified as pending or reviewer
// unavailability counts — an unrecognized or empty class keeps the harshest
// exit code, which is the conservative direction for a gate. A downgrade must
// be something the code deliberately decided, never something it forgot.
func hasUnclassifiedOrCode(bs []MergeBlocker) bool {
	for _, b := range bs {
		if b.Class != ClassPending && b.Class != ClassReviewerUnavailable {
			return true
		}
	}
	return false
}

// botBodies returns every automated-review body on the PR — CodeRabbit posts
// its walkthrough as an issue comment, but replies to `@coderabbitai review`
// can land as reviews, so both surfaces are scanned.
func botBodies(pr ghPRView) []string {
	out := []string{}
	for _, c := range pr.Comments {
		if isReviewBot(c.Author.Login) {
			out = append(out, c.Body)
		}
	}
	for _, r := range pr.Reviews {
		if isReviewBot(r.Author.Login) {
			out = append(out, r.Body)
		}
	}
	return out
}

func isReviewBot(login string) bool {
	l := strings.ToLower(login)
	return strings.Contains(l, "coderabbit")
}

// bodiesCoverHead reports whether any review body names the current head sha.
// CodeRabbit prints the exact base..head range it reviewed, so an absent head
// sha means the review predates the newest push.
func bodiesCoverHead(bodies []string, head string) bool {
	head = strings.ToLower(head)
	for _, b := range bodies {
		for _, sha := range sha40.FindAllString(strings.ToLower(b), -1) {
			if sha == head {
				return true
			}
		}
	}
	return false
}

func describeOrState(c ghCheck) string {
	if strings.TrimSpace(c.Description) != "" {
		return c.Description
	}
	if c.State != "" {
		return c.State
	}
	return "no description"
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// FormatMergeGate renders the human report. Blockers are the point of the
// output, so they lead.
func FormatMergeGate(pr ghPRView, v MergeGateVerdict) string {
	var b strings.Builder
	head := fmt.Sprintf("PR #%d", pr.Number)
	if pr.URL != "" {
		head = pr.URL
	}
	switch {
	case v.Merged && v.OriginLagsLive:
		// The whole point of robots-oex0: "MERGED" alone is what a mechanic
		// converts into "FIXED", and here that conversion is invalid.
		fmt.Fprintf(&b, "MERGED — BUT NOT LIVE (origin lags the deployed branch) — %s\n", head)
	case v.Merged:
		fmt.Fprintf(&b, "MERGED — %s\n", head)
	case v.Ready && v.OriginLagsLive:
		fmt.Fprintf(&b, "READY TO MERGE — BUT MERGING WILL NOT MAKE IT LIVE — %s\n", head)
	case v.Ready:
		fmt.Fprintf(&b, "READY — %s\n", head)
	case v.NeedsDecision:
		fmt.Fprintf(&b, "NEEDS-DECISION (%d) — %s\n", len(v.Blockers), head)
	case v.Pending:
		fmt.Fprintf(&b, "PENDING (%d) — %s\n", len(v.Blockers), head)
	default:
		fmt.Fprintf(&b, "BLOCKED (%d) — %s\n", len(v.Blockers), head)
	}
	for _, bl := range v.Blockers {
		fmt.Fprintf(&b, "  ✗ %-20s %s\n", bl.Code, bl.Detail)
	}
	for _, n := range v.Notes {
		fmt.Fprintf(&b, "  · %s\n", n)
	}
	if v.Ready && !v.Merged {
		b.WriteString("  · Checks green, a real review covered the current head, no unresolved threads.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// MergeGate is the IO wrapper: fetch, decide, print, exit.
func MergeGate(argv []string) {
	if helpWanted("merge-gate", argv) {
		return
	}
	r := args.Parse("merge-gate", argv, []string{"--json"}, []string{"--repo"})

	if len(r.Positionals) == 0 {
		httpc.Die("parlay merge-gate: need a PR number, e.g. merge-gate 45 [--repo owner/name]", config.ExitUsage)
		return
	}
	prNum, err := strconv.Atoi(strings.TrimPrefix(r.Positionals[0], "#"))
	if err != nil || prNum <= 0 {
		httpc.Die(fmt.Sprintf("parlay merge-gate: %q is not a PR number", r.Positionals[0]), config.ExitUsage)
		return
	}
	repoFlag, _ := r.String("--repo")

	repo, repoSource, err := resolveMergeGateRepo(repoFlag)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay merge-gate: %v", err), config.ExitRuntime)
		return
	}

	snap, err := fetchMergeGateSnapshot(repo, repoSource, prNum)
	if err != nil {
		// Could not answer — exit 1, distinct from a real block, still non-zero.
		httpc.Die(fmt.Sprintf("parlay merge-gate: %v", err), config.ExitRuntime)
		return
	}

	v := ComputeMergeGate(snap)

	if r.Bool("--json") {
		out, _ := json.MarshalIndent(struct {
			PR         int              `json:"pr"`
			URL        string           `json:"url"`
			Repo       string           `json:"repo"`
			RepoSource string           `json:"repoSource"`
			Verdict    MergeGateVerdict `json:"verdict"`
			Checks     []ghCheck        `json:"checks"`
			Live       LiveBranchState  `json:"live"`
			Unresolv   int              `json:"unresolvedThreads"`
		}{snap.PR.Number, snap.PR.URL, snap.Repo, snap.RepoSource, v, snap.Checks, snap.Live, snap.UnresolvedThreads}, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Println(FormatMergeGate(snap.PR, v))
	}

	if v.ExitCode != config.ExitOK {
		httpc.Exit(v.ExitCode)
	}
}

// prViewFields is the exact --json field set fetchMergeGateSnapshot requests.
const prViewFields = "number,url,state,mergeable,mergeStateStatus,headRefOid,baseRefName,author,reviews,comments"

// reviewThreadsQuery counts unresolved review threads. `gh pr view` has no
// field for thread resolution, so this is the one place GraphQL is needed.
const reviewThreadsQuery = `query($o:String!,$r:String!,$n:Int!){repository(owner:$o,name:$r){pullRequest(number:$n){reviewThreads(first:100){nodes{isResolved}}}}}`

// fetchMergeGateSnapshot reads the PR. `repo` is the already-resolved
// "owner/name" from resolveMergeGateRepo and is passed to EVERY gh call, so
// the three sources that make up one verdict can never disagree about which
// repository they describe.
func fetchMergeGateSnapshot(repo, repoSource string, pr int) (MergeGateSnapshot, error) {
	var s MergeGateSnapshot
	s.Repo, s.RepoSource = repo, repoSource

	viewArgs := []string{"pr", "view", strconv.Itoa(pr), "--json", prViewFields}
	if repo != "" {
		viewArgs = append(viewArgs, "--repo", repo)
	}
	res := sh("gh", viewArgs...)
	if !res.ok {
		return s, fmt.Errorf("could not read PR #%d in %s: %s", pr, repoLabel(repo), firstLine(res.err))
	}
	if err := json.Unmarshal([]byte(res.out), &s.PR); err != nil {
		return s, fmt.Errorf("could not parse `gh pr view` output: %w", err)
	}

	// `gh pr checks` exits non-zero whenever any check is failing or pending,
	// which is a normal input to this gate — read stdout regardless of code,
	// and only treat unparseable output as an error.
	checkArgs := []string{"pr", "checks", strconv.Itoa(pr), "--json", "name,state,bucket,description"}
	if repo != "" {
		checkArgs = append(checkArgs, "--repo", repo)
	}
	cres := sh("gh", checkArgs...)
	if strings.TrimSpace(cres.out) != "" {
		if err := json.Unmarshal([]byte(cres.out), &s.Checks); err != nil {
			return s, fmt.Errorf("could not parse `gh pr checks` output: %w", err)
		}
	}
	// No stdout means gh reported "no checks reported" — leave Checks empty
	// so the no-checks blocker fires, rather than erroring out.

	// Local, read-only, and never fatal: a checkout that cannot answer this
	// leaves Live.Known false and the gate simply says nothing about liveness.
	s.Live = detectLiveBranchDrift(s.PR.BaseRefName)

	if owner, name, ok := splitRepo(repo); ok {
		q := sh("gh", "api", "graphql", "-f", "query="+reviewThreadsQuery,
			"-F", "o="+owner, "-F", "r="+name, "-F", "n="+strconv.Itoa(pr),
			"--jq", "[.data.repository.pullRequest.reviewThreads.nodes[]|select(.isResolved|not)]|length")
		if q.ok {
			if n, err := strconv.Atoi(strings.TrimSpace(q.out)); err == nil {
				s.UnresolvedThreads, s.ThreadsKnown = n, true
			}
		}
	}
	return s, nil
}

// detectLiveBranchDrift measures the local base branch against
// origin/<base>. Every failure path returns Known=false rather than a zero
// count: "could not tell" and "they agree" are different answers, and only
// one of them licenses the merged-means-fixed inference.
//
// Refs are shared across every linked worktree of a repo, so this reads the
// same answer from a mechanic's isolated worktree as from the primary
// checkout — which matters, because the contract sends every mechanic into a
// worktree. It never checks anything out and never writes.
func detectLiveBranchDrift(base string) LiveBranchState {
	st := LiveBranchState{Branch: strings.TrimSpace(base)}
	if st.Branch == "" {
		return st
	}
	if res := sh("git", "rev-parse", "--is-inside-work-tree"); !res.ok || strings.TrimSpace(res.out) != "true" {
		return st
	}
	local := "refs/heads/" + st.Branch
	remote := "refs/remotes/origin/" + st.Branch
	// A repo with no local copy of the base branch has no deployed branch to
	// disagree with origin, and one with no remote-tracking ref has nothing
	// to compare against. Neither is a defect; both are unknowable.
	if r := sh("git", "rev-parse", "--verify", "--quiet", local); !r.ok {
		return st
	}
	if r := sh("git", "rev-parse", "--verify", "--quiet", remote); !r.ok {
		return st
	}
	// --left-right --count over the symmetric difference: left = commits only
	// on origin (local is behind), right = commits only on local (origin lags).
	r := sh("git", "rev-list", "--left-right", "--count", remote+"..."+local)
	if !r.ok {
		return st
	}
	f := strings.Fields(r.out)
	if len(f) != 2 {
		return st
	}
	behind, err1 := strconv.Atoi(f[0])
	ahead, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return st
	}
	st.Known, st.Behind, st.Ahead = true, behind, ahead
	return st
}

// splitRepo splits "owner/name" (tolerating a trailing .git and any leading
// host/path segments). Pure — repo resolution lives in resolveMergeGateRepo.
func splitRepo(repo string) (owner, name string, ok bool) {
	repo = strings.TrimSuffix(strings.TrimSpace(repo), ".git")
	parts := strings.Split(strings.Trim(repo, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-1] == "" || parts[len(parts)-2] == "" {
		return "", "", false
	}
	return parts[len(parts)-2], parts[len(parts)-1], true
}

// resolveMergeGateRepo decides, ONCE, which repository this run is about.
//
// This is the robots-g4qz defect. Letting `gh` pick the repo implicitly is
// not neutral: gh's base-repo resolution deliberately prefers a remote named
// `upstream` over `origin` (its remote sort order is upstream, github,
// origin, …). In a fork clone — which is every clone the fleet works in,
// origin=trillium/<repo> with an `upstream` remote pointing at the project
// it was forked from — `parlay merge-gate 2` therefore read UPSTREAM's PR #2
// while the mechanic was asking about the fork's. The numbers collide freely
// between the two repositories, and the failure is silent: the gate prints a
// perfectly well-formed verdict about somebody else's pull request. Worst
// case it is exit 0 "PR is already MERGED — nothing left to gate" for an
// upstream PR that landed months ago, which is a gate that fails OPEN on an
// unreviewed, still-open fork PR. That is the exact thing this verb exists
// to prevent.
//
// Precedence, highest first:
//
//  1. an explicit --repo — the caller said it, so it wins outright;
//  2. the `origin` remote — for the fleet, origin is where the PR under
//     review lives, and it is the same remote the mechanic contract's
//     `git branch -r --contains <sha>` proof checks against;
//  3. gh's own resolution — only for a checkout with no usable origin (or no
//     git repo at all), where gh's guess is the only thing available.
//
// The chosen repo and the step that chose it are both reported, so a wrong
// answer is visible in the output rather than inferred from a URL nobody
// reads.
func resolveMergeGateRepo(explicit string) (repo, source string, err error) {
	if strings.TrimSpace(explicit) != "" {
		if _, _, ok := splitRepo(explicit); !ok {
			return "", "", fmt.Errorf("--repo %q is not owner/name", explicit)
		}
		return strings.TrimSuffix(strings.TrimSpace(explicit), ".git"), "--repo", nil
	}

	if res := sh("git", "remote", "get-url", "origin"); res.ok {
		if r, ok := repoFromRemoteURL(res.out); ok {
			return r, "origin remote", nil
		}
	}

	// No origin to trust. Fall back to gh, which is what this verb always did
	// — but say so, because this is the branch that can pick `upstream`.
	res := sh("gh", "repo", "view", "--json", "owner,name",
		"--jq", `.owner.login + "/" + .name`)
	if !res.ok {
		return "", "", fmt.Errorf(
			"could not determine which repository to gate (no `origin` remote here, and `gh repo view` failed: %s). Pass --repo owner/name",
			firstLine(res.err))
	}
	r := strings.TrimSpace(res.out)
	if _, _, ok := splitRepo(r); !ok {
		return "", "", fmt.Errorf("could not determine which repository to gate (`gh repo view` returned %q). Pass --repo owner/name", r)
	}
	return r, "gh default (no origin remote)", nil
}

// remoteURLRe pulls owner/name out of any git remote URL shape: the scp-like
// git@host:owner/name.git, https://host/owner/name(.git), ssh://git@host/…,
// and git://host/…. Only the last two path segments matter, so host and
// credentials are deliberately not modeled.
var remoteURLRe = regexp.MustCompile(`^(?:[a-z+]+://)?(?:[^/@]+@)?[^/:]+[:/](.+?)(?:\.git)?/?$`)

// repoFromRemoteURL converts a git remote URL to "owner/name".
func repoFromRemoteURL(url string) (string, bool) {
	m := remoteURLRe.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", false
	}
	owner, name, ok := splitRepo(m[1])
	if !ok {
		return "", false
	}
	return owner + "/" + name, true
}

// repoFromPRURL converts a PR's html_url to "owner/name" so the answer can be
// checked against the repository that was actually asked about.
func repoFromPRURL(u string) (string, bool) {
	i := strings.Index(u, "/pull/")
	if i < 0 {
		return "", false
	}
	owner, name, ok := splitRepo(u[:i])
	if !ok {
		return "", false
	}
	return owner + "/" + name, true
}

func repoLabel(repo string) string {
	if strings.TrimSpace(repo) == "" {
		return "the current repository"
	}
	return repo
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s = strings.TrimSpace(s); s == "" {
		return "gh returned no error text"
	}
	return s
}
