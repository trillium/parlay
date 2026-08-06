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
//  1. the PR is open, not conflicting, and not behind its base — a check that
//     ran against an older base has not evaluated the merge that would happen,
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
// A green check is also only evidence about the base it ran against
// (robots-1hs5). GitHub runs `pull_request` workflows on refs/pull/N/merge —
// the merge RESULT, not the branch head — and it recomputes that ref when the
// base branch moves, but it does NOT re-trigger the check run. So a PR whose
// CI went green hours ago keeps that green forever, describing a merge with a
// main that no longer exists. Merging it lands a combination no CI ever
// evaluated. trillium/firstmate #76/#77/#79 each stayed green that way and
// collectively broke main with duplicate entries in a shared JSON file
// (robots-ot20) — three PRs that were individually correct and jointly wrong.
//
// The obvious signal does not work: `mergeStateStatus=BEHIND` is only ever
// reported when the base branch has protection with "require branches to be up
// to date before merging" enabled, and the repos this gate runs against have no
// protection at all — every behind PR there reports CLEAN or UNSTABLE. Reading
// that field alone would be a blocker that never fires in exactly the case it
// was written for. So the gate asks the compare API instead
// (`repos/O/R/compare/base...head` -> `behind_by`), which is true regardless of
// repo settings, and treats BEHIND as corroboration when it is present.
//
// Being behind is code-class (3): it is fixable on the branch by merging the
// base in or rebasing, which re-triggers CI against the current base. The gate
// deliberately does NOT try to time-correlate check completion against the base
// tip's commit date to exempt "behind but CI ran after the base moved" — commit
// dates are not push times and can predate the push arbitrarily, so that
// refinement fails OPEN on exactly the force-pushed and rebased branches it
// would matter for. Blocking every behind PR is the same rule GitHub's own
// "require branches to be up to date" enforces, and it costs one rebase.

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
	BaseRefName      string      `json:"baseRefName"`
	HeadRefOid       string      `json:"headRefOid"`
	Author           ghAuthor    `json:"author"`
	Reviews          []ghReview  `json:"reviews"`
	Comments         []ghComment `json:"comments"`
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
	// UnresolvedThreads counts review threads still marked unresolved.
	UnresolvedThreads int
	// ThreadsKnown is false when the review-thread query could not be run;
	// the gate then reports the gap instead of silently claiming zero.
	ThreadsKnown bool
	// BehindBy is how many commits the base branch has that the head does not
	// — i.e. how stale the base this PR's checks ran against now is
	// (robots-1hs5). Read from the compare API, NOT from mergeStateStatus,
	// which only says BEHIND on a protected branch that requires up-to-date
	// branches. BehindKnown is false when that call could not be made; the
	// gate then reports the gap rather than claiming zero.
	BehindBy    int
	BehindKnown bool
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
	Pending  bool           `json:"pending"`
	Blockers []MergeBlocker `json:"blockers"`
	Notes    []string       `json:"notes"`
	ExitCode int            `json:"exitCode"`
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

	// --- base freshness -----------------------------------------------
	//
	// Everything below this point reasons about checks and reviews, and both
	// of them are statements about a merge with SOME base. If that base has
	// moved, every one of those statements is about a merge that will not
	// happen (robots-1hs5). Nothing re-runs on a base move, so the gate is
	// the only thing that can notice.
	baseLabel := s.PR.BaseRefName
	if baseLabel == "" {
		baseLabel = "the base branch"
	}
	switch {
	case s.BehindKnown && s.BehindBy > 0:
		block(&v, "behind-base",
			"%s has %d commit(s) this branch does not — every check here ran against a merge with an older %s, and GitHub does not re-run them when the base moves. Merge %s in (or rebase) so CI evaluates the code that would actually land.",
			baseLabel, s.BehindBy, baseLabel, baseLabel)
	case strings.EqualFold(s.PR.MergeStateStatus, "BEHIND"):
		// Only reachable on a protected base that requires up-to-date
		// branches; kept because when it IS present it is authoritative, and
		// it is the one path that still works if the compare call failed.
		block(&v, "behind-base",
			"GitHub reports mergeStateStatus=BEHIND: this branch is out of date with %s, so its green checks describe a merge with an older base. Merge %s in (or rebase).",
			baseLabel, baseLabel)
	case !s.BehindKnown:
		v.Notes = append(v.Notes,
			"Could not compare this branch against its base — whether the checks ran against the CURRENT base is UNKNOWN, not confirmed.")
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
	case v.Merged:
		fmt.Fprintf(&b, "MERGED — %s\n", head)
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
		b.WriteString("  · Checks green against the current base, a real review covered the current head, no unresolved threads.\n")
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
			Unresolv   int              `json:"unresolvedThreads"`
			// behindBy is null, not 0, when the compare call could not be
			// made — "unknown" and "up to date" must not look identical to a
			// scripted caller.
			BehindBy *int `json:"behindBy"`
		}{snap.PR.Number, snap.PR.URL, snap.Repo, snap.RepoSource, v, snap.Checks, snap.UnresolvedThreads,
			behindByJSON(snap)}, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Println(FormatMergeGate(snap.PR, v))
	}

	if v.ExitCode != config.ExitOK {
		httpc.Exit(v.ExitCode)
	}
}

// behindByJSON reports how far behind the base the branch is, or nil when the
// gate could not find out. A scripted caller must be able to tell "0 commits
// behind" from "never asked".
func behindByJSON(s MergeGateSnapshot) *int {
	if !s.BehindKnown {
		return nil
	}
	n := s.BehindBy
	return &n
}

// prViewFields is the exact --json field set fetchMergeGateSnapshot requests.
const prViewFields = "number,url,state,mergeable,mergeStateStatus,baseRefName,headRefOid,author,reviews,comments"

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

	// How far the base has moved since this PR's checks ran (robots-1hs5).
	// `gh pr view` cannot answer this — mergeStateStatus only reports BEHIND
	// on a protected branch that requires up-to-date branches — so ask the
	// compare API, which is true on any repo. Best-effort: a failure leaves
	// BehindKnown false and the gate discloses the gap instead of assuming
	// the branch is current. Pinned to the resolved repo like every other gh
	// call here (robots-g4qz).
	if s.PR.BaseRefName != "" && s.PR.HeadRefOid != "" && repo != "" {
		c := sh("gh", "api", fmt.Sprintf("repos/%s/compare/%s...%s", repo, s.PR.BaseRefName, s.PR.HeadRefOid),
			"--jq", ".behind_by")
		if c.ok {
			if n, err := strconv.Atoi(strings.TrimSpace(c.out)); err == nil {
				s.BehindBy, s.BehindKnown = n, true
			}
		}
	}

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
