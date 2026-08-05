// Tests for the merge gate's decision layer (robots-jap6). Every case is a
// pure ComputeMergeGate call over a hand-built snapshot — no gh binary, no
// network — so the regressions that matter (a vacuous green check reading as
// mergeable) are pinned independently of how the data is fetched.
package commands

import (
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// realReviewBody is a trimmed copy of the shape CodeRabbit posts once a
// review has genuinely run: the walkthrough marker plus the explicit
// base..head range it processed.
func realReviewBody(base, head string) string {
	return "<!-- This is an auto-generated comment: summarize by coderabbit.ai -->\n" +
		"<summary>📥 Commits</summary>\n" +
		"Reviewing files that changed from the base of the PR and between " + base + " and " + head + ".\n" +
		"<!-- walkthrough_start -->\n## Walkthrough\nThe server now centralizes persistence paths.\n"
}

// rateLimitedBody is the template CodeRabbit posts INSTEAD of a review when
// the account-wide PR limit is hit — the exact case that produced a green
// check on PR #45.
const rateLimitedBody = "<!-- This is an auto-generated comment: summarize by coderabbit.ai -->\n" +
	"<!-- This is an auto-generated comment: rate limited by coderabbit.ai -->\n\n" +
	"> [!WARNING]\n> ## Review limit reached\n>\n> `@trillium`, you've reached your PR review limit, " +
	"so we couldn't start this review.\n"

// rateLimitedWithReviewDetailsBody is the REAL body CodeRabbit posted on this
// fix's own PR (#47), trimmed. The refusal template is not a bare error: it
// embeds a "Review details" section enumerating the files and the exact
// base..head range it WOULD have processed. An earlier version of this gate
// treated "Files selected for processing" as review evidence and so read this
// refusal as a completed review of the current head — the very false-green it
// exists to stop.
func rateLimitedWithReviewDetailsBody(base, head string) string {
	return "<!-- This is an auto-generated comment: summarize by coderabbit.ai -->\n" +
		"<!-- This is an auto-generated comment: rate limited by coderabbit.ai -->\n\n" +
		"> [!WARNING]\n> ## Review limit reached\n>\n> `@trillium`, you've reached your PR review limit, " +
		"so we couldn't start this review.\n>\n> **Next review available in:** **51 minutes**\n>\n" +
		"> <details>\n> <summary>Review details</summary>\n>\n" +
		"> <summary>📥 Commits</summary>\n" +
		"> Reviewing files that changed from the base of the PR and between " + base + " and " + head + ".\n" +
		"> <summary>📒 Files selected for processing (7)</summary>\n" +
		"> * `tools/cli/main.go`\n> </details>\n"
}

const headSHA = "b124d8f769d5d25cc29d162ec5ee79181f15a1e1"
const baseSHA = "3dc74abe3208aa8f3d12250c0764b664225f268e"

// reviewedPR is a snapshot that should pass every rule, so each test can
// mutate exactly one thing and attribute the resulting blocker to it.
func reviewedPR() MergeGateSnapshot {
	return MergeGateSnapshot{
		PR: ghPRView{
			Number:           45,
			URL:              "https://github.com/trillium/parlay/pull/45",
			State:            "OPEN",
			Mergeable:        "MERGEABLE",
			MergeStateStatus: "CLEAN",
			HeadRefOid:       headSHA,
			Author:           ghAuthor{Login: "trillium"},
			Comments: []ghComment{
				{Author: ghAuthor{Login: "coderabbitai"}, Body: realReviewBody(baseSHA, headSHA)},
			},
		},
		Checks:       []ghCheck{{Name: "CodeRabbit", State: "SUCCESS", Bucket: "pass", Description: "1 file reviewed"}},
		ThreadsKnown: true,
	}
}

func blockerCodes(v MergeGateVerdict) []string {
	out := make([]string, 0, len(v.Blockers))
	for _, b := range v.Blockers {
		out = append(out, b.Code)
	}
	return out
}

func hasBlocker(v MergeGateVerdict, code string) bool {
	for _, b := range v.Blockers {
		if b.Code == code {
			return true
		}
	}
	return false
}

func TestReviewedPRWithGreenChecksIsReady(t *testing.T) {
	v := ComputeMergeGate(reviewedPR())
	if !v.Ready {
		t.Fatalf("expected READY, got blockers %v", blockerCodes(v))
	}
	if v.ExitCode != config.ExitOK {
		t.Errorf("ExitCode = %d, want %d", v.ExitCode, config.ExitOK)
	}
}

// The robots-jap6 regression itself: CodeRabbit hit its account-wide limit,
// never reviewed, and STILL reported bucket=pass. Before this gate existed,
// `gh pr checks 45` said "CodeRabbit pass 0 Review rate limited" and
// mergeStateStatus said CLEAN, so a mechanic auto-merged unreviewed code.
func TestRateLimitedCheckIsNotAPassEvenThoughGitHubSaysPass(t *testing.T) {
	s := reviewedPR()
	s.Checks = []ghCheck{{Name: "CodeRabbit", State: "SUCCESS", Bucket: "pass", Description: "Review rate limited"}}
	s.PR.Comments = []ghComment{{Author: ghAuthor{Login: "coderabbitai"}, Body: rateLimitedBody}}

	v := ComputeMergeGate(s)
	if v.Ready {
		t.Fatal("a rate-limited CodeRabbit run must NOT read as ready to merge")
	}
	if !hasBlocker(v, "vacuous-pass") {
		t.Errorf("want vacuous-pass blocker, got %v", blockerCodes(v))
	}
	if !hasBlocker(v, "review-rate-limited") {
		t.Errorf("want review-rate-limited blocker, got %v", blockerCodes(v))
	}
	// Both blockers are the reviewer not participating, so this is the
	// needs-decision case (robots-8kkq), not a hard block — still non-zero.
	if v.ExitCode != ExitMergeNeedsDecision {
		t.Errorf("ExitCode = %d, want %d", v.ExitCode, ExitMergeNeedsDecision)
	}
}

// robots-8kkq: exit 3 alone gave a mechanic no terminating condition — it
// says "do not merge" and nothing about whether waiting will ever help. When
// the ONLY blockers are the reviewer being unavailable, the gate must say so
// distinctly and hand over the two honest options.
func TestReviewerUnavailableIsNeedsDecisionNotAHardBlock(t *testing.T) {
	s := reviewedPR()
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pass", Description: "Review rate limited"}}
	s.PR.Comments = []ghComment{{Author: ghAuthor{Login: "coderabbitai"}, Body: rateLimitedBody}}

	v := ComputeMergeGate(s)
	if v.Ready {
		t.Fatal("needs-decision must never read as ready")
	}
	if !v.NeedsDecision {
		t.Fatalf("want NeedsDecision, got blockers %v", blockerCodes(v))
	}
	if v.ExitCode != ExitMergeNeedsDecision {
		t.Errorf("ExitCode = %d, want %d", v.ExitCode, ExitMergeNeedsDecision)
	}
	if v.ExitCode == config.ExitOK {
		t.Error("needs-decision must stay non-zero so a naive caller still fails closed")
	}
	for _, b := range v.Blockers {
		if b.Class != ClassReviewerUnavailable {
			t.Errorf("blocker %q classed %q, want %q", b.Code, b.Class, ClassReviewerUnavailable)
		}
	}
	notes := strings.Join(v.Notes, " ")
	for _, want := range []string{"merge-and-disclose", "park", "needs-decision", "unbounded"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes should name %q so the caller has a bounded answer, got %v", want, v.Notes)
		}
	}
}

// One real finding alongside the rate limit is still a hard block: the
// downgrade must not become a way to launder a failing test into "the
// captain's call".
func TestOneCodeBlockerKeepsTheWholeVerdictHardBlocked(t *testing.T) {
	s := reviewedPR()
	s.PR.Comments = []ghComment{{Author: ghAuthor{Login: "coderabbitai"}, Body: rateLimitedBody}}
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pass", Description: "Review rate limited"},
		{Name: "build", Bucket: "fail", Description: "2 tests failed"},
	}

	v := ComputeMergeGate(s)
	if v.NeedsDecision {
		t.Fatalf("a failing check must not be downgraded to needs-decision, got %v", blockerCodes(v))
	}
	if v.ExitCode != ExitMergeBlocked {
		t.Errorf("ExitCode = %d, want %d", v.ExitCode, ExitMergeBlocked)
	}
}

// No review evidence at all is NOT reviewer-unavailability: the gate cannot
// tell why nothing reviewed the PR, so it keeps the harsher code.
func TestUnexplainedMissingReviewStaysAHardBlock(t *testing.T) {
	s := reviewedPR()
	s.PR.Comments = nil
	v := ComputeMergeGate(s)
	if v.NeedsDecision {
		t.Fatalf("an unexplained missing review must not be downgraded, got %v", blockerCodes(v))
	}
	if v.ExitCode != ExitMergeBlocked {
		t.Errorf("ExitCode = %d, want %d", v.ExitCode, ExitMergeBlocked)
	}
}

// The trillium/no-mistakes#7 shape: `@coderabbitai review` recovered the
// first push, then the account stayed limited, so the follow-up commit
// merged unreviewed. A stale review PLUS a live rate-limit template is the
// reviewer being unavailable, not something the branch can fix.
func TestStaleReviewUnderAnActiveRateLimitIsReviewerUnavailable(t *testing.T) {
	s := reviewedPR()
	s.PR.HeadRefOid = "0000000000000000000000000000000000000000"
	s.PR.Comments = append(s.PR.Comments,
		ghComment{Author: ghAuthor{Login: "coderabbitai"}, Body: rateLimitedBody})

	v := ComputeMergeGate(s)
	if !hasBlocker(v, "stale-review") {
		t.Fatalf("want stale-review, got %v", blockerCodes(v))
	}
	if !v.NeedsDecision || v.ExitCode != ExitMergeNeedsDecision {
		t.Errorf("stale review under an active rate limit should be needs-decision, got %+v", v)
	}
}

// Without a rate limit, a stale review is ordinary code-class work: push
// again and the reviewer catches up. It must NOT reach the captain.
func TestStaleReviewWithoutARateLimitStaysAHardBlock(t *testing.T) {
	s := reviewedPR()
	s.PR.HeadRefOid = "0000000000000000000000000000000000000000"

	v := ComputeMergeGate(s)
	if v.NeedsDecision {
		t.Fatalf("a plain stale review is fixable on the branch, got %v", blockerCodes(v))
	}
	if v.ExitCode != ExitMergeBlocked {
		t.Errorf("ExitCode = %d, want %d", v.ExitCode, ExitMergeBlocked)
	}
}

func TestNeedsDecisionVerdictLeadsWithItsOwnHeader(t *testing.T) {
	s := reviewedPR()
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pass", Description: "Review rate limited"}}
	s.PR.Comments = []ghComment{{Author: ghAuthor{Login: "coderabbitai"}, Body: rateLimitedBody}}

	out := FormatMergeGate(s.PR, ComputeMergeGate(s))
	if !strings.HasPrefix(out, "NEEDS-DECISION") {
		t.Errorf("report should lead with NEEDS-DECISION, got:\n%s", out)
	}
}

// The refusal template lists the files and the base..head range it WOULD
// have reviewed, so a content match for "files selected for processing"
// classifies a refusal as a review OF THE CURRENT HEAD — passing both the
// review-evidence and staleness rules. Found live on PR #47, this fix's own
// PR: only the vacuous-pass rule caught it, and on a repo whose check
// description were less honest nothing would have.
func TestRateLimitTemplateIsNeverReviewEvidenceDespiteListingFiles(t *testing.T) {
	s := reviewedPR()
	s.PR.Comments = []ghComment{{
		Author: ghAuthor{Login: "coderabbitai"},
		Body:   rateLimitedWithReviewDetailsBody(baseSHA, headSHA),
	}}
	// Description deliberately honest-looking, so ONLY the review-evidence
	// rule can catch this — isolating the regression from vacuous-pass.
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pass", Description: "Review completed"}}

	v := ComputeMergeGate(s)
	if v.Ready {
		t.Fatal("a rate-limit refusal that lists files must NOT count as a review")
	}
	if !hasBlocker(v, "review-rate-limited") {
		t.Errorf("want review-rate-limited, got %v", blockerCodes(v))
	}
}

// The description is the only truthful field, so the gate must key off it
// even when the conclusion, state, and mergeability all look perfect.
func TestVacuousPassDescriptionVariants(t *testing.T) {
	for _, desc := range []string{
		"Review rate limited",
		"review rate-limited",
		"Review limit reached",
		"Review skipped",
		"Skipping review for this PR",
		"Files not reviewed",
	} {
		s := reviewedPR()
		s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pass", Description: desc}}
		if v := ComputeMergeGate(s); !hasBlocker(v, "vacuous-pass") {
			t.Errorf("description %q should be a vacuous pass, got %v", desc, blockerCodes(v))
		}
	}
}

// A description that merely mentions review counts must stay green — the
// gate is worthless if it blocks every real pass too.
func TestGenuinePassDescriptionsStayGreen(t *testing.T) {
	for _, desc := range []string{
		"1 file reviewed",
		"Review completed",
		"10 actionable comments posted",
		"",
	} {
		s := reviewedPR()
		s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pass", Description: desc}}
		if v := ComputeMergeGate(s); hasBlocker(v, "vacuous-pass") {
			t.Errorf("description %q should NOT be a vacuous pass", desc)
		}
	}
}

func TestFailingAndPendingChecksBlock(t *testing.T) {
	s := reviewedPR()
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pending", Description: "Review in progress"}}
	if v := ComputeMergeGate(s); !hasBlocker(v, "check-pending") {
		t.Errorf("pending check should block, got %v", blockerCodes(v))
	}

	s.Checks = []ghCheck{{Name: "build", Bucket: "fail", Description: "2 tests failed"}}
	if v := ComputeMergeGate(s); !hasBlocker(v, "check-failed") {
		t.Errorf("failing check should block, got %v", blockerCodes(v))
	}
}

// In this repo CodeRabbit is the ONLY check — there are no .github/workflows
// at all — so "zero checks" is a real, reachable state that must not read as
// "nothing failed, therefore fine".
func TestNoChecksAtAllBlocks(t *testing.T) {
	s := reviewedPR()
	s.Checks = nil
	if v := ComputeMergeGate(s); !hasBlocker(v, "no-checks") {
		t.Errorf("a PR with no checks should block, got %v", blockerCodes(v))
	}
}

func TestNoReviewEvidenceBlocks(t *testing.T) {
	s := reviewedPR()
	s.PR.Comments = nil
	v := ComputeMergeGate(s)
	if !hasBlocker(v, "no-review-evidence") {
		t.Errorf("want no-review-evidence, got %v", blockerCodes(v))
	}
}

// The check conclusion goes green on the newest push whether or not the
// review re-ran, so a review of an older commit is another way the gate
// would otherwise wave through unreviewed code.
func TestReviewOfAnEarlierCommitIsStale(t *testing.T) {
	s := reviewedPR()
	s.PR.HeadRefOid = "0000000000000000000000000000000000000000"
	v := ComputeMergeGate(s)
	if !hasBlocker(v, "stale-review") {
		t.Errorf("want stale-review, got %v", blockerCodes(v))
	}
}

func TestHumanReviewCountsAsReviewEvidence(t *testing.T) {
	s := reviewedPR()
	s.PR.Comments = nil
	s.PR.Reviews = []ghReview{{Author: ghAuthor{Login: "someone-else"}, State: "APPROVED"}}

	v := ComputeMergeGate(s)
	if !v.Ready {
		t.Fatalf("a human-approved PR should be ready, got %v", blockerCodes(v))
	}
	if len(v.Notes) == 0 || !strings.Contains(strings.Join(v.Notes, " "), "someone-else") {
		t.Errorf("verdict should name the human reviewer, notes = %v", v.Notes)
	}
}

// A self-review is not review; the author approving their own PR must not
// satisfy the evidence rule.
func TestSelfReviewIsNotReviewEvidence(t *testing.T) {
	s := reviewedPR()
	s.PR.Comments = nil
	s.PR.Reviews = []ghReview{{Author: ghAuthor{Login: "trillium"}, State: "APPROVED"}}

	if v := ComputeMergeGate(s); !hasBlocker(v, "no-review-evidence") {
		t.Errorf("self-review should not count, got %v", blockerCodes(v))
	}
}

// The second known lie: the check stays green regardless of finding count,
// so unresolved threads have to be read separately.
func TestUnresolvedThreadsBlock(t *testing.T) {
	s := reviewedPR()
	s.UnresolvedThreads = 3
	v := ComputeMergeGate(s)
	if !hasBlocker(v, "unresolved-threads") {
		t.Fatalf("want unresolved-threads, got %v", blockerCodes(v))
	}
	if !strings.Contains(v.Blockers[0].Detail, "3 review thread") {
		t.Errorf("blocker should name the count, got %q", v.Blockers[0].Detail)
	}
}

// An unanswerable thread query must be reported as UNKNOWN rather than
// silently counted as zero — silence is the failure mode this whole ticket
// is about.
func TestUnknownThreadCountIsNotedNotAssumedZero(t *testing.T) {
	s := reviewedPR()
	s.ThreadsKnown = false
	v := ComputeMergeGate(s)
	if !v.Ready {
		t.Fatalf("an unknown thread count should not itself block, got %v", blockerCodes(v))
	}
	if !strings.Contains(strings.Join(v.Notes, " "), "UNKNOWN") {
		t.Errorf("want an UNKNOWN note, got %v", v.Notes)
	}
}

func TestConflictingPRBlocks(t *testing.T) {
	s := reviewedPR()
	s.PR.Mergeable = "CONFLICTING"
	if v := ComputeMergeGate(s); !hasBlocker(v, "conflicting") {
		t.Errorf("want conflicting, got %v", blockerCodes(v))
	}
}

func TestMergedPRShortCircuitsToReady(t *testing.T) {
	s := reviewedPR()
	s.PR.State = "MERGED"
	s.Checks = nil // already landed — check state no longer means anything
	v := ComputeMergeGate(s)
	if !v.Ready || !v.Merged {
		t.Fatalf("a merged PR should report merged+ready, got %+v", v)
	}
	if len(v.Blockers) != 0 {
		t.Errorf("a merged PR should have no blockers, got %v", blockerCodes(v))
	}
}

func TestClosedUnmergedPRBlocks(t *testing.T) {
	s := reviewedPR()
	s.PR.State = "CLOSED"
	v := ComputeMergeGate(s)
	if v.Ready || !hasBlocker(v, "pr-closed") {
		t.Errorf("want pr-closed, got %+v", v)
	}
}

func TestFormatLeadsWithTheVerdictAndNamesEveryBlocker(t *testing.T) {
	s := reviewedPR()
	// A code-class blocker, so this exercises the BLOCKED header specifically;
	// the NEEDS-DECISION header has its own test.
	s.Checks = []ghCheck{{Name: "build", Bucket: "fail", Description: "2 tests failed"}}
	s.PR.Mergeable = "CONFLICTING"

	v := ComputeMergeGate(s)
	out := FormatMergeGate(s.PR, v)
	if !strings.HasPrefix(out, "BLOCKED") {
		t.Errorf("report should lead with the verdict, got:\n%s", out)
	}
	for _, code := range blockerCodes(v) {
		if !strings.Contains(out, code) {
			t.Errorf("report omits blocker %q:\n%s", code, out)
		}
	}
}

func TestSplitRepoParsesOwnerAndName(t *testing.T) {
	owner, name, ok := splitRepo("trillium/parlay")
	if !ok || owner != "trillium" || name != "parlay" {
		t.Errorf("splitRepo(trillium/parlay) = %q %q %v", owner, name, ok)
	}
	if _, _, ok := splitRepo("no-slash"); ok {
		t.Error("splitRepo should reject a repo with no owner")
	}
}
