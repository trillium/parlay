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
// code is found, 5 when the reviewer simply has not finished yet, 6 when the
// only red is a check that failed without ever running the code, 4 when the
// only thing missing is a reviewer that is unavailable, 1 when gh/network
// could not answer, 2 on usage. A caller that just branches on non-zero
// refuses the merge in all six failure modes, which is the correct default
// for a gate.
//
// 4 exists because 3 alone gave the fleet no bounded answer (robots-8kkq).
// "CodeRabbit is rate limited" and "a test is failing" are both non-zero, but
// they call for opposite behavior: the second is fixed by working on the PR,
// while the first cannot be fixed from the PR at all. A mechanic told only
// "blocked" polls forever. Splitting the exit code lets the caller stop with a
// terminating condition instead of burning the night on a wait.
//
// What 4 must NOT do is escalate before spending the one cheap action that can
// change the answer (task-6ch1h). For a long time this verb told the caller
// that re-requesting "has stayed limited across repeated attempts" and to hand
// the choice to the captain without trying it. That advice rested on a
// diagnosis nobody had checked, and it was wrong twice over:
//
//   - Automatic review is not merely unreliable here, it is OFF. CodeRabbit
//     says so in plain text on any PR that asks: "This repository does not
//     receive automatic reviews because it has fewer than 10 stars." So
//     "park until the reviewer returns" was never a terminating strategy —
//     nobody was coming.
//   - An explicit `@coderabbitai review` DOES work. #122 came back "Action
//     performed: Review finished" within a minute and found a real goroutine
//     and fd leak in that PR's own new code.
//
// Six PRs (#116–#121) were merged unreviewed under the old advice, each with a
// no-review disclosure, when one comment apiece would have gotten a real
// review. That is the cost of a gate that hands back a decision it could have
// resolved. The notes on the 4 path now spend the re-request first and escalate
// only after its stated window has lapsed.
//
// The rate limit is real, though — it is just a quota, not a refusal. The free
// tier includes roughly one review per hour and the reply states the wait
// ("Next included review available in NN minutes"), so re-requests have to be
// SEQUENCED across PRs. Firing three at once spends the window on whichever
// lands first; #123 and #124 both got "Action not completed — Review rate
// limited" that way.
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
// 6 exists because a check can also fail WITHOUT EVER RUNNING THE CODE
// (robots-6mw2). GitHub Actions jobs die during action setup —
//
//	##[error]Failed to resolve action download info. Error: Service Unavailable
//	##[error]Service Unavailable
//
// — before a single line of the repo executes, and the check reports
// bucket=fail with an empty description, indistinguishable by status alone
// from a genuinely failing test. Three trillium/firstmate runs failed that way
// in one afternoon, and every PR open at the time showed red that had nothing
// to do with its diff. Landing that in 3 tells a mechanic to "fix it on the
// branch" — hunting a defect in code that never executed, on a branch whose
// only real problem is GitHub's availability. This is the exact sibling of the
// vacuous pass: a check that failed without running says as little about the
// diff as a check that passed without running.
//
// The discriminator is the check run's ANNOTATIONS, not its description (which
// GitHub Actions leaves empty). An infra death annotates only GitHub's own
// errors; a real failure always annotates "Process completed with exit code N"
// from the step that ran. So the downgrade requires positive evidence — at
// least one infra annotation and no annotation that looks like the code
// failing — and anything unreadable stays code-class.
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
//
// The same skepticism the gate applies to a check CONCLUSION it now applies to
// head FRESHNESS (robots-bn5d). Every assertion above is evaluated against
// origin's `headRefOid` — which is correct, since that is the commit a merge
// would land — but the caller is a mechanic who has just authored a fix and
// pushed it, and for whom READY reads as "my fix is cleared to merge". On
// trillium/firstmate#91 the fix had gone to the no-mistakes MIRROR and the
// pipeline had not yet pushed it to origin, so origin's head was still the
// pre-fix commit: the gate said READY, and merging there would have landed the
// old head and silently DROPPED the just-authored fix for the reviewer's
// finding. Minutes later, once the pipeline did push, the same PR went 0 -> 4.
// So the READY was not merely early, it was the opposite of the eventual
// truthful answer. The gate cannot see a mirror, but it can see that the local
// branch holds commits origin's PR head does not — which is the same fact from
// the only side it has access to — so an unpushed local head is a PENDING
// blocker, and origin's head sha is printed on every verdict so the
// discrepancy is visible even where the gate cannot check.

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

// vacuousCheckDesc matches a status-check description that ADMITS the check
// did no work, even though its conclusion is green. The description is the
// only field CodeRabbit fills in truthfully when it is rate limited, so it is
// the field this gate reads.
var vacuousCheckDesc = regexp.MustCompile(`(?i)rate[ -]?limit|limit reached|review skipped|skipping review|not reviewed|never ran|review unavailable`)

// infraAnnotation matches a check-run annotation that names a GITHUB-side
// failure — the job died in the runner or during action setup, before any of
// this repository's code ran. Deliberately narrow: every entry here is a
// message GitHub's own infrastructure emits, never something a repo's test
// harness prints. A test that legitimately fails while asserting on, say, a
// 503 response still annotates "Process completed with exit code 1", which is
// not in this set and therefore keeps the whole check code-class.
var infraAnnotation = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`failed to resolve action download info`,
	`unable to resolve action`,
	`service unavailable`,
	`internal server error`,
	`bad gateway`,
	`gateway time-?out`,
	`received a shutdown signal`,
	`lost communication with the server`,
	`the runner has received`,
	`not acquired by runner`,
	`you have exceeded a secondary rate limit`,
}, "|"))

// Deliberately NOT in that set: "The job has exceeded the maximum execution
// time of …". A job that ran out of wall clock DID run this repository's code
// — an infinite loop or a hung test in the diff produces exactly that
// annotation — so it stays code-class even though a starved runner can produce
// it too. Fail closed: the gate may send a mechanic to look at a timeout that
// turns out to be GitHub's fault, but it must never wave off a hang that is
// the diff's fault.

// checkRunIDRe pulls the check-run id out of a GitHub Actions check link
// (.../actions/runs/<run>/job/<id>). For Actions, the job id in that URL IS
// the check-run id the annotations API takes.
var checkRunIDRe = regexp.MustCompile(`/(?:job|check-runs)/(\d+)`)

// actionsRunIDRe pulls the workflow RUN id out of the same link, so the gate
// can print the exact `gh run rerun` command rather than a shape to fill in.
var actionsRunIDRe = regexp.MustCompile(`/actions/runs/(\d+)`)

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

// rateLimitMinutes pulls the wait out of CodeRabbit's rate-limit template.
//
// This is the difference between a wait and a decision. A quota with a stated
// expiry has a terminating condition — come back then; a quota with an unknown
// one does not, and only the second is worth a captain's attention. The gate
// already had this number sitting in the comment body and was throwing it away,
// which is why the advice on the exit-4 path could only ever say "after the
// window" without saying when that was.
//
// The pattern is loose in three specific places, because CodeRabbit has used at
// least two spellings and this file has a fixture of each:
//
//	**Next review available in:** **51 minutes**      (PR #47, older)
//	Next included review available in 57 minutes.     (PR #123, today)
//
// So "included" is optional, the colon is optional, and stray `*` emphasis is
// tolerated around the number. Singular "minute" is matched too: the template
// does not switch wording at 1, and a gate that silently reports no window on
// the last minute of the wait is worse than one that never reported it.
//
// Tightening this to one exact sentence is the failure mode to avoid — the
// wording has already changed once, and a miss here reads as "no window
// stated", which escalates a wait into a captain's decision.
var rateLimitMinutes = regexp.MustCompile(`(?i)next\s+(?:included\s+)?review\s+available\s+in[\s:*]*(\d+)[\s*]*minutes?`)

// rateLimitWindow returns the stated wait as a human string, or "" when no body
// carries one.
//
// It returns the LARGEST window across the bodies rather than the first. There
// is normally exactly one, but CodeRabbit edits its comment in place while
// separate `@coderabbitai review` replies accumulate as their own comments, so
// a PR that has been re-requested more than once can carry several. The largest
// is the conservative pick: coming back too late costs one gate re-run, coming
// back too early spends a re-request that is refused.
func rateLimitWindow(bodies []string) string {
	best := 0
	for _, b := range bodies {
		m := rateLimitMinutes.FindStringSubmatch(b)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			// Unreachable via the pattern (\d+ only), but a parse that fails
			// must not be read as "no window" — that is the direction that
			// turns a wait back into an escalation.
			continue
		}
		if n > best {
			best = n
		}
	}
	if best == 0 {
		return ""
	}
	unit := "minutes"
	if best == 1 {
		unit = "minute"
	}
	return fmt.Sprintf("%d %s", best, unit)
}

// requestReviewCmd builds the exact re-request command, repo flag included.
//
// The `--repo` is not optional politeness. AGENTS.md forbids letting gh pick
// the repo implicitly, because it prefers a remote named `upstream` over
// `origin` — so a bare `gh pr comment 122` on a fork posts to someone else's
// repository. A gate that prints a command a caller will paste has to print the
// safe spelling of it.
func requestReviewCmd(repo string, number int) string {
	cmd := fmt.Sprintf("gh pr comment %d", number)
	if repo != "" {
		cmd += " --repo " + repo
	}
	return cmd + " --body '@coderabbitai review'"
}

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

// ghCheck is one row of `gh pr checks --json name,state,bucket,description,link`.
// Bucket is gh's normalization of the many per-provider states into
// pass/fail/pending/skipping/cancel.
type ghCheck struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Bucket      string `json:"bucket"`
	Description string `json:"description"`
	// Link is the check's target URL. For GitHub Actions it is
	// .../actions/runs/<run>/job/<checkRunID>, which is the only place the
	// annotations API's id is available from a `gh pr checks` row.
	Link string `json:"link"`
	// Annotations are the check run's own annotations, fetched separately for
	// failing checks only. AnnotationsKnown distinguishes "fetched, and there
	// were none" from "could not read them" — the second must never be
	// mistaken for the first, since only positive evidence may downgrade a
	// failure out of code class.
	Annotations      []ghAnnotation `json:"annotations,omitempty"`
	AnnotationsKnown bool           `json:"annotationsKnown,omitempty"`
}

// ghAnnotation is one entry of `gh api repos/<repo>/check-runs/<id>/annotations`.
type ghAnnotation struct {
	Level   string `json:"annotation_level"`
	Message string `json:"message"`
	Title   string `json:"title"`
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
	HeadRefName      string      `json:"headRefName"`
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

// Relation names how a local branch sits against origin's PR head.
const (
	RelationUnknown  = "unknown"
	RelationSame     = "same"
	RelationAhead    = "ahead"
	RelationBehind   = "behind"
	RelationDiverged = "diverged"
)

// HeadFreshness compares the LOCAL copy of the PR's head branch against the
// `headRefOid` GitHub reports — the difference between "the fix I just wrote"
// and "the commit a merge would actually land" (robots-bn5d).
//
// Everything else this gate asserts is about origin's head, and correctly so.
// But the caller has just authored a fix and pushed it, and reads READY as a
// verdict on THAT. When the push went somewhere origin has not caught up with
// yet — the no-mistakes mirror, whose pipeline pushes to origin asynchronously
// — origin's head is still the pre-fix commit. Merging then lands the old head
// and silently drops the fix, which is the exact premature-FIXED failure the
// mechanic guardrails exist to prevent.
//
// The gate has no way to see a mirror or a pipeline run. What it CAN see, from
// any checkout or linked worktree of the repo, is that the local branch
// contains commits origin's PR head does not — the same fact observed from the
// side the gate has access to, and true for a plain forgotten `git push` as
// well. That is the signal; the pipeline is only one way to produce it.
type HeadFreshness struct {
	// Known is false when the comparison could not be made at all (not in a
	// git work tree, origin points somewhere else, no local branch of that
	// name, or origin's head sha is not in the local object store). The gate
	// then says so explicitly rather than implying agreement — "could not
	// tell" and "they agree" are different answers.
	Known bool `json:"known"`
	// Branch is the PR's head branch, e.g. "fix/robots-u7gu".
	Branch string `json:"branch"`
	// LocalHead is the sha the local branch points at.
	LocalHead string `json:"localHead"`
	// Relation is how LocalHead sits against origin's headRefOid: same, ahead,
	// behind, diverged, or unknown.
	Relation string `json:"relation"`
	// Ahead counts commits on the local branch that origin's PR head does not
	// have — i.e. how much of the local work a merge would drop.
	Ahead int `json:"ahead"`
	// Reason explains a Known=false answer, so the gap is legible.
	Reason string `json:"reason,omitempty"`
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
	// Head is the local-vs-origin head comparison. Zero value (Known=false)
	// means it could not be measured, which the gate reports rather than
	// treating as agreement.
	Head HeadFreshness
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

// ComputeMergeGate is the whole decision, as a pure function of a snapshot.
func ComputeMergeGate(s MergeGateSnapshot) MergeGateVerdict {
	v := MergeGateVerdict{Blockers: []MergeBlocker{}, Notes: []string{}, BehindKnown: s.BehindKnown}

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
	// Head freshness runs BEFORE the MERGED short-circuit, because a PR merged
	// at a head the local branch is ahead of is not a resolved question — it is
	// this defect already realized, with the fix dropped and the merge sitting
	// there looking like proof it landed (robots-bn5d).
	assessHeadFreshness(&v, s)

	switch strings.ToUpper(s.PR.State) {
	case "MERGED":
		// Already landed. Nothing left to gate — say so plainly rather than
		// running checks that no longer mean anything. The one exception is a
		// head-freshness blocker: that one is about whether WHAT landed is what
		// the caller thinks landed, which merging does not answer.
		v.Merged = true
		v.Notes = append(v.Notes, "PR is already MERGED — nothing left to gate.")
		if len(v.Blockers) == 0 {
			v.Ready, v.ExitCode = true, config.ExitOK
		} else {
			v.ExitCode = ExitMergeBlocked
		}
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
	rerunHint := ""
	for _, c := range s.Checks {
		name := c.Name
		if name == "" {
			name = "(unnamed check)"
		}
		switch strings.ToLower(c.Bucket) {
		case "fail", "cancel":
			// A failing check is a finding about the diff ONLY if it got far
			// enough to have an opinion about it (robots-6mw2). classifyFailedCheck
			// demands positive evidence for anything softer, so an unreadable
			// or ambiguous failure stays code-class.
			class, why := classifyFailedCheck(c)
			if class == ClassInfra {
				if m := actionsRunIDRe.FindStringSubmatch(c.Link); m != nil && rerunHint == "" {
					rerunHint = m[1]
				}
				blockAs(&v, "check-did-not-run", ClassInfra,
					"check %q is %s, but %s — GitHub-side, not a finding about this diff.",
					name, c.Bucket, why)
				break
			}
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
	bodies := botBodies(s.PR)
	reviewedBodies := []string{}
	rateLimited := false
	for _, body := range bodies {
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
		// The template states the wait, so quote it back rather than making the
		// caller open the PR to find out how long "after the window" is. A quota
		// with a stated expiry is a wait; a quota with an unknown one is a
		// decision, and telling them apart is the whole point of exit 4.
		msg := "CodeRabbit posted its rate-limit template and never reviewed this PR."
		if w := rateLimitWindow(bodies); w != "" {
			msg += " It says the next included review is available in " + w + "."
		}
		blockAs(&v, "review-rate-limited", ClassReviewerUnavailable,
			"%s Re-request after that window with `%s`, or enable usage-based reviews.",
			msg, requestReviewCmd(s.Repo, s.PR.Number))
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

	// Class precedence, harshest first: code (3) > pending (5) > infra (6) >
	// reviewer unavailable (4). Each arm only runs when no harsher class is
	// present.
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
			"If the reply is \"Review rate limited\", that is a quota, not a refusal: the free tier includes roughly one review per hour, and the reply states the wait (\"Next included review available in NN minutes\"). Re-request after that window rather than escalating. Sequence re-requests across PRs — firing them at three PRs at once spends the window on whichever lands first and the other two get nothing.",
			"Only once a re-request has been made AND its stated window has lapsed is this a real decision: signal `parlay status needs-decision` and let the captain choose merge-and-disclose (land it, stating plainly in the merge note that no review ran) or park. Do not escalate before spending the re-request — that is handing back a decision the gate could have resolved for the price of one comment.")
	}
	return v
}

// classifyFailedCheck decides whether a failing or cancelled check is a
// statement about THIS DIFF or about GitHub (robots-6mw2).
//
// The check row itself cannot answer that: a GitHub Actions check reports
// bucket=fail with an EMPTY description whether a test failed or the runner
// could not download an action. The check run's annotations can. A job that
// ran the repo's code and failed always annotates the step's exit
// ("Process completed with exit code 1"); a job that died in setup annotates
// only GitHub's own error text.
//
// The downgrade is deliberately evidence-gated in both directions: it needs at
// least one infra annotation AND no annotation that looks like the code
// failing. Unreadable annotations, an empty annotation list on a failed job, or
// any unrecognized failure text all keep the check code-class — the
// conservative direction for a gate, and the same rule the rest of this file
// follows.
//
// A CANCELLED job is the one case that needs no annotation: cancellation is by
// definition an ending before a verdict, so it is never evidence about the
// code. In practice it is the cascade half of this same incident — GitHub
// cancels the remaining jobs of a run whose siblings died in setup. A real
// failure alongside it still keeps its own code class and, by precedence,
// keeps the whole verdict at 3.
func classifyFailedCheck(c ghCheck) (class string, why string) {
	cancelled := strings.EqualFold(strings.TrimSpace(c.Bucket), "cancel")
	cancelWhy := "the job was cancelled and never reported on this code"

	if !c.AnnotationsKnown {
		if cancelled {
			return ClassInfra, cancelWhy
		}
		return ClassCode, ""
	}

	infraMsg := ""
	sawCodeEvidence := false
	for _, a := range c.Annotations {
		// Only failure-level annotations are evidence. Warnings (deprecated
		// runner images, and so on) sit on perfectly healthy jobs.
		if !strings.EqualFold(a.Level, "failure") {
			continue
		}
		msg := strings.TrimSpace(a.Message + " " + a.Title)
		if infraAnnotation.MatchString(msg) {
			if infraMsg == "" {
				infraMsg = strings.TrimSpace(firstLine(a.Message))
			}
			continue
		}
		sawCodeEvidence = true
	}

	switch {
	case sawCodeEvidence:
		return ClassCode, ""
	case infraMsg != "":
		return ClassInfra, fmt.Sprintf("nothing in this repo ran: it died in GitHub with %q", infraMsg)
	case cancelled:
		return ClassInfra, cancelWhy
	default:
		return ClassCode, ""
	}
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

// assessHeadFreshness reports what the local checkout knows about the commit a
// merge would actually land, and blocks when that commit is missing work the
// local branch already has (robots-bn5d).
//
// Origin's `headRefOid` is printed on every verdict, unconditionally. That is
// the minimum the caller needs: the whole failure was a mechanic reading READY
// as a verdict on the fix they had just written, when the gate had in fact
// answered about the commit before it, and nothing in the output named which
// commit that was.
//
// The blocker is deliberately PENDING-class while the PR is open. Nothing is
// wrong with the diff; the commits simply have not arrived at origin yet, and
// the answer changes on its own once they do — exactly the "not yet" that exit
// 5 exists for. On a MERGED PR it is code-class instead: the merge already
// happened at the stale head, so no amount of waiting fixes it and the work has
// to be pushed and re-landed.
func assessHeadFreshness(v *MergeGateVerdict, s MergeGateSnapshot) {
	head := strings.TrimSpace(s.PR.HeadRefOid)
	if head == "" {
		return
	}
	label := shortSHA(head)
	if b := strings.TrimSpace(s.PR.HeadRefName); b != "" {
		label = fmt.Sprintf("%s on %s", shortSHA(head), b)
	}
	h := s.Head
	merged := strings.EqualFold(s.PR.State, "MERGED")

	if !h.Known {
		reason := h.Reason
		if reason == "" {
			reason = "no local copy of this branch to compare against"
		}
		// "Could not tell" is not "they agree". Say which commit the verdict is
		// about and hand the caller the one-line check the gate could not run.
		// On a MERGED PR there is nothing left to do "before merging" — the
		// same doubt is about what already landed, so say that instead.
		advice := fmt.Sprintf(
			"If you just pushed a fix for this PR, check it yourself before merging: `gh pr view %d --json headRefOid` must match your local HEAD. A push that has not reached origin yet leaves every verdict above describing the PRE-fix commit (robots-bn5d).",
			s.PR.Number)
		if merged {
			advice = "That is the commit that MERGED. If you had a fix in flight for this PR, confirm it is in there — `git branch -r --contains <your sha>` must list origin's default branch. A push that never reached origin means the merge landed the PRE-fix commit and dropped your work (robots-bn5d)."
		}
		v.Notes = append(v.Notes, fmt.Sprintf(
			"origin head: %s — NOT verified against a local branch (%s). %s",
			label, reason, advice))
		return
	}

	switch {
	case h.Ahead > 0:
		what := "would merge"
		if merged {
			what = "merged"
		}
		detail := fmt.Sprintf(
			"local %s is %d commit(s) ahead of origin's PR head %s — those commits are NOT in what %s. If you just pushed through a pipeline (`git push no-mistakes`), the run has not reached origin yet; if you pushed nowhere, they are only on this machine.",
			h.Branch, h.Ahead, shortSHA(head), what)
		if h.Relation == RelationDiverged {
			detail += " The two have also DIVERGED — origin's head is not an ancestor of the local branch, so this is not a plain pending push."
		}
		if merged {
			// Past tense: the stale merge already happened, and the mechanic
			// contract's `git branch -r --contains <sha>` proof will pass for
			// the wrong commit. Nothing transient about it.
			block(v, "head-not-pushed", "%s", detail)
			return
		}
		blockAs(v, "head-not-pushed", ClassPending, "%s", detail)
	case h.Relation == RelationBehind:
		// Harmless for merging — origin simply has commits this checkout has
		// not fetched. Worth naming so it is not mistaken for the case above.
		v.Notes = append(v.Notes, fmt.Sprintf(
			"origin head: %s — local %s is BEHIND it (nothing local is at risk; this checkout is just stale).", label, h.Branch))
	default:
		v.Notes = append(v.Notes, fmt.Sprintf(
			"origin head: %s — local %s agrees, so this verdict is about the commit you have.", label, h.Branch))
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
	case v.Infra:
		fmt.Fprintf(&b, "INFRA (%d) — %s\n", len(v.Blockers), head)
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
		// Name the commit. READY is a verdict about origin's head and nothing
		// else, and the whole robots-bn5d failure was a mechanic reading it as
		// a verdict on the fix they had just pushed somewhere origin had not
		// caught up with. The sha is the one thing that makes the two
		// distinguishable at a glance.
		if v.BehindKnown {
			fmt.Fprintf(&b, "  · Checks green against the current base, a real review covered origin's head %s, no unresolved threads. Merging lands THAT commit — confirm it is the one you mean.\n",
				shortSHA(pr.HeadRefOid))
		} else {
			fmt.Fprintf(&b, "  · Checks green (base freshness unknown — could not compare branch against base), a real review covered origin's head %s, no unresolved threads. Merging lands THAT commit — confirm it is the one you mean.\n",
				shortSHA(pr.HeadRefOid))
		}
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
			HeadOid    string           `json:"headRefOid"`
			Head       HeadFreshness    `json:"headFreshness"`
			Unresolv   int              `json:"unresolvedThreads"`
			// behindBy is null, not 0, when the compare call could not be
			// made — "unknown" and "up to date" must not look identical to a
			// scripted caller.
			BehindBy *int `json:"behindBy"`
		}{snap.PR.Number, snap.PR.URL, snap.Repo, snap.RepoSource, v, snap.Checks, snap.Live, snap.PR.HeadRefOid, snap.Head, snap.UnresolvedThreads,
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
const prViewFields = "number,url,state,mergeable,mergeStateStatus,baseRefName,headRefOid,headRefName,author,reviews,comments"

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
	checkArgs := []string{"pr", "checks", strconv.Itoa(pr), "--json", "name,state,bucket,description,link"}
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

	// Only failing checks need annotations, and only they pay for the extra
	// API call — a green PR still costs exactly the same three requests it
	// always did.
	for i := range s.Checks {
		switch strings.ToLower(s.Checks[i].Bucket) {
		case "fail", "cancel":
			loadCheckAnnotations(repo, &s.Checks[i])
		}
	}

	// Local, read-only, and never fatal: a checkout that cannot answer this
	// leaves Live.Known false and the gate simply says nothing about liveness.
	s.Live = detectLiveBranchDrift(s.PR.BaseRefName)
	// Local, read-only, and never fatal: a checkout that cannot answer this
	// leaves Head.Known false, and the gate says so instead of implying the
	// local branch agrees.
	s.Head = detectHeadFreshness(repo, s.PR.HeadRefName, s.PR.HeadRefOid)

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

// annotationPageSize is what loadCheckAnnotations asks for, and also its
// truncation tripwire: a full page might have a second page behind it holding
// the one annotation that would have proved the check code-class, so a full
// page is treated as unreadable rather than paginated. Real jobs annotate a
// handful of lines; this only ever fires on pathological output.
const annotationPageSize = 100

// loadCheckAnnotations fills in a failing check's annotations in place, which
// is what lets classifyFailedCheck tell a GitHub-side death from a real
// failure (robots-6mw2). Every failure path leaves AnnotationsKnown false, so
// an unreachable API, an unparseable body, a non-Actions check, or a
// suspiciously full page all keep the check code-class.
func loadCheckAnnotations(repo string, c *ghCheck) {
	if repo == "" {
		return
	}
	m := checkRunIDRe.FindStringSubmatch(c.Link)
	if m == nil {
		// Not a GitHub Actions check (CodeRabbit's link is empty, third-party
		// checks point at their own dashboards) — there is no annotations
		// endpoint to ask, so this stays a code-class failure.
		return
	}
	res := sh("gh", "api", fmt.Sprintf("repos/%s/check-runs/%s/annotations?per_page=%d", repo, m[1], annotationPageSize))
	if !res.ok {
		return
	}
	var anns []ghAnnotation
	if err := json.Unmarshal([]byte(res.out), &anns); err != nil {
		return
	}
	if len(anns) >= annotationPageSize {
		return
	}
	c.Annotations, c.AnnotationsKnown = anns, true
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

// detectHeadFreshness measures the local copy of the PR's head branch against
// origin's `headRefOid` (robots-bn5d).
//
// Every failure path returns Known=false with a Reason rather than a zero
// count: "could not tell" and "they agree" are different answers, and only one
// of them licenses reading READY as a verdict on the fix you just wrote.
//
// It is pinned to the repo the gate already resolved (robots-g4qz): a checkout
// whose `origin` is a different repository can hold a same-named branch, and
// comparing against that would invent a blocker out of an unrelated project.
// Refs are shared across every linked worktree of a repo, so this reads the
// same answer from a mechanic's isolated worktree as from the primary
// checkout — which matters, because the contract sends every mechanic into a
// worktree. It never checks anything out, never fetches, and never writes.
func detectHeadFreshness(repo, branch, originHead string) HeadFreshness {
	st := HeadFreshness{Branch: strings.TrimSpace(branch), Relation: RelationUnknown}
	unknown := func(format string, a ...any) HeadFreshness {
		st.Reason = fmt.Sprintf(format, a...)
		return st
	}

	if st.Branch == "" {
		return unknown("GitHub did not report a head branch name")
	}
	originHead = strings.ToLower(strings.TrimSpace(originHead))
	if originHead == "" {
		return unknown("GitHub did not report a head sha")
	}
	if res := sh("git", "rev-parse", "--git-dir"); !res.ok {
		return unknown("not inside a git work tree")
	}

	// Pin to the same repository every gh call was pinned to.
	local, ok := "", false
	if res := sh("git", "remote", "get-url", "origin"); res.ok {
		local, ok = repoFromRemoteURL(res.out)
	}
	switch {
	case !ok:
		return unknown("this checkout has no usable `origin` remote")
	case repo != "" && !strings.EqualFold(local, repo):
		return unknown("this checkout's origin is %s, not %s", local, repo)
	}

	res := sh("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+st.Branch)
	if !res.ok || strings.TrimSpace(res.out) == "" {
		return unknown("no local branch %q in this checkout", st.Branch)
	}
	st.LocalHead = strings.ToLower(strings.TrimSpace(res.out))

	if st.LocalHead == originHead {
		st.Known, st.Relation = true, RelationSame
		return st
	}
	// Counting needs origin's head in this object store. A branch that has
	// never been fetched cannot be compared — and saying so names the fix.
	if res := sh("git", "cat-file", "-e", originHead+"^{commit}"); !res.ok {
		return unknown("origin's head %s is not in this checkout's object store (run `git fetch origin`)", shortSHA(originHead))
	}

	// --left-right against origin's head: left = commits only origin has
	// (this checkout is behind), right = commits only the local branch has
	// (what a merge at origin's head would drop).
	counts := sh("git", "rev-list", "--count", "--left-right", originHead+"..."+st.LocalHead)
	if !counts.ok {
		return unknown("could not compare local %s against origin's head", st.Branch)
	}
	fields := strings.Fields(counts.out)
	if len(fields) != 2 {
		return unknown("could not parse the local/origin commit counts")
	}
	behind, err1 := strconv.Atoi(fields[0])
	ahead, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		return unknown("could not parse the local/origin commit counts")
	}

	st.Known, st.Ahead = true, ahead
	switch {
	case ahead > 0 && behind > 0:
		st.Relation = RelationDiverged
	case ahead > 0:
		st.Relation = RelationAhead
	case behind > 0:
		st.Relation = RelationBehind
	default:
		st.Relation = RelationSame
	}
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
