// Tests for the merge gate's decision layer (robots-jap6). Every case is a
// pure ComputeMergeGate call over a hand-built snapshot — no gh binary, no
// network — so the regressions that matter (a vacuous green check reading as
// mergeable) are pinned independently of how the data is fetched.
package commands

import (
	"os"
	"os/exec"
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
			HeadRefName:      "fix/robots-jap6-merge-gate",
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
// The robots-rwf8 regression, reproduced exactly as observed on
// trillium/no-mistakes#11: CodeRabbit's check is still running, so it has
// posted no review comment yet — `check-pending` PLUS `no-review-evidence`.
// That pairing exited 3, the code the mechanic contract documents as "blocked
// on the CODE, fix it on the branch". Nothing was wrong with the branch; the
// same unchanged PR exited 0 minutes later. It must be exit 5.
func TestReviewStillRunningIsPendingNotBlockedOnTheCode(t *testing.T) {
	s := reviewedPR()
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pending", Description: "Review in progress"}}
	s.PR.Comments = nil // the review has not posted anything yet

	v := ComputeMergeGate(s)
	if !hasBlocker(v, "check-pending") || !hasBlocker(v, "no-review-evidence") {
		t.Fatalf("want the observed pair, got %v", blockerCodes(v))
	}
	if v.ExitCode == ExitMergeBlocked {
		t.Fatalf("a review that has not finished is not a defect in the code; exit 3 sends the mechanic to edit a clean branch")
	}
	if !v.Pending || v.ExitCode != ExitMergePending {
		t.Errorf("ExitCode = %d (pending=%v), want %d", v.ExitCode, v.Pending, ExitMergePending)
	}
	if v.Ready {
		t.Error("pending must stay non-zero so a naive caller still fails closed")
	}
	if v.NeedsDecision {
		t.Error("nothing needs deciding while the answer is still arriving")
	}
	for _, b := range v.Blockers {
		if b.Class != ClassPending {
			t.Errorf("blocker %q classed %q, want %q", b.Code, b.Class, ClassPending)
		}
	}
	notes := strings.Join(v.Notes, " ")
	for _, want := range []string{"Re-run", "not a finding about this code", "Do NOT edit"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes should say %q so the caller waits instead of editing, got %v", want, v.Notes)
		}
	}
}

func TestPendingVerdictLeadsWithItsOwnHeader(t *testing.T) {
	s := reviewedPR()
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pending", Description: "Review in progress"}}
	s.PR.Comments = nil

	out := FormatMergeGate(s.PR, ComputeMergeGate(s))
	if !strings.HasPrefix(out, "PENDING") {
		t.Errorf("report should lead with PENDING, not BLOCKED, got:\n%s", out)
	}
}

// The pending downgrade must never launder a real finding. A failing check
// alongside a running one is still exit 3 — the same guarantee that already
// protects the needs-decision downgrade.
func TestOneCodeBlockerOutranksPending(t *testing.T) {
	s := reviewedPR()
	s.PR.Comments = nil
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pending", Description: "Review in progress"},
		{Name: "build", Bucket: "fail", Description: "2 tests failed"},
	}

	v := ComputeMergeGate(s)
	if v.Pending || v.ExitCode != ExitMergeBlocked {
		t.Errorf("a failing check must not be downgraded to pending, got exit %d over %v", v.ExitCode, blockerCodes(v))
	}
}

// An unresolved review thread is a finding somebody wrote about this code; a
// second check still running does not make it go away.
func TestUnresolvedThreadsOutrankPending(t *testing.T) {
	s := reviewedPR()
	s.UnresolvedThreads = 2
	s.Checks = append(s.Checks, ghCheck{Name: "build", Bucket: "pending", Description: "Running"})

	v := ComputeMergeGate(s)
	if v.Pending || v.ExitCode != ExitMergeBlocked {
		t.Errorf("unresolved threads must stay a hard block, got exit %d over %v", v.ExitCode, blockerCodes(v))
	}
}

// Pending outranks reviewer-unavailable: asking the captain to choose
// merge-and-disclose while another check is mid-flight is a decision made on
// information that is about to arrive. Re-run instead; it will resolve into a
// real 0/3/4.
func TestPendingOutranksReviewerUnavailable(t *testing.T) {
	s := reviewedPR()
	s.PR.Comments = nil
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pass", Description: "Review rate limited"},
		{Name: "build", Bucket: "pending", Description: "Running"},
	}

	v := ComputeMergeGate(s)
	if v.NeedsDecision {
		t.Fatalf("do not escalate to the captain while a check is still running, got %v", blockerCodes(v))
	}
	if !v.Pending || v.ExitCode != ExitMergePending {
		t.Errorf("ExitCode = %d, want %d", v.ExitCode, ExitMergePending)
	}
}

// A stale review while a check is running is the re-review in flight — "not
// yet", not "push again to fix it".
func TestStaleReviewWhileAReviewIsRunningIsPending(t *testing.T) {
	s := reviewedPR()
	s.PR.HeadRefOid = "0000000000000000000000000000000000000000"
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pending", Description: "Review in progress"}}

	v := ComputeMergeGate(s)
	if !hasBlocker(v, "stale-review") {
		t.Fatalf("want stale-review, got %v", blockerCodes(v))
	}
	if !v.Pending || v.ExitCode != ExitMergePending {
		t.Errorf("ExitCode = %d, want %d", v.ExitCode, ExitMergePending)
	}
}

// A live rate-limit refusal outranks an unfinished check: that reviewer has
// already answered, and the answer was "no".
func TestRateLimitedStaleReviewStaysNeedsDecisionEvenWithAPendingCheck(t *testing.T) {
	s := reviewedPR()
	s.PR.HeadRefOid = "0000000000000000000000000000000000000000"
	s.PR.Comments = append(s.PR.Comments,
		ghComment{Author: ghAuthor{Login: "coderabbitai"}, Body: rateLimitedBody})
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pending", Description: "Review in progress"}}

	v := ComputeMergeGate(s)
	for _, b := range v.Blockers {
		if b.Code == "stale-review" && b.Class != ClassReviewerUnavailable {
			t.Errorf("stale-review classed %q, want %q — a live refusal outranks a running check", b.Class, ClassReviewerUnavailable)
		}
	}
}

// The robots-eowy regression, exactly as observed on trillium/no-mistakes#13:
// CodeRabbit reviewed the FIRST push, the branch moved, and the re-review was
// refused. Because CodeRabbit edits its one comment in place, the walkthrough
// body from that first review is still sitting on the PR and the refusal
// exists ONLY in the check description — so the comment-only rate-limit signal
// never fires, `stale-review` stayed code-class, and the gate printed
// `vacuous-pass` + `stale-review` and exited 3. Exit 3 means "fix it on the
// branch": the mechanic goes hunting a defect that does not exist, and each
// edit it pushes restarts the review and re-consumes the limit.
func TestVacuousCheckAloneMakesAStaleReviewReviewerUnavailable(t *testing.T) {
	s := reviewedPR()
	s.PR.HeadRefOid = "685efaf2fe6d6c1a4b7e1c9d5d2e3f4a5b6c7d8e"
	// No rate-limit COMMENT anywhere — the in-place-edited body is still the
	// real review of the earlier push.
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pass", Description: "Review rate limited"}}

	v := ComputeMergeGate(s)
	if got := blockerCodes(v); len(got) != 2 || !hasBlocker(v, "vacuous-pass") || !hasBlocker(v, "stale-review") {
		t.Fatalf("expected the no-mistakes#13 shape (vacuous-pass + stale-review), got %v", got)
	}
	for _, b := range v.Blockers {
		if b.Class != ClassReviewerUnavailable {
			t.Errorf("blocker %q classed %q, want %q — nothing here is about the diff", b.Code, b.Class, ClassReviewerUnavailable)
		}
	}
	if !v.NeedsDecision || v.ExitCode != ExitMergeNeedsDecision {
		t.Errorf("ExitCode = %d, want %d — a refused re-review is not a defect in the branch", v.ExitCode, ExitMergeNeedsDecision)
	}
}

// The same signal on the other arm: "nothing reviewed this PR" was kept
// code-class only because the gate could not tell WHY. A check that says it
// did not run IS the why, so the reason governs and this reaches the captain
// instead of sending a mechanic to edit unobjected-to code.
func TestVacuousCheckExplainsAnAbsentReview(t *testing.T) {
	s := reviewedPR()
	s.PR.Comments = nil
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pass", Description: "Review rate limited"}}

	v := ComputeMergeGate(s)
	if !hasBlocker(v, "no-review-evidence") {
		t.Fatalf("want no-review-evidence, got %v", blockerCodes(v))
	}
	if !v.NeedsDecision || v.ExitCode != ExitMergeNeedsDecision {
		t.Errorf("ExitCode = %d, want %d", v.ExitCode, ExitMergeNeedsDecision)
	}
}

// A missing review with a GREEN check is still unexplained — the reason has to
// be stated, not merely absent. This is the guard that keeps the reclassify
// narrow.
func TestGreenCheckDoesNotExplainAnAbsentReview(t *testing.T) {
	s := reviewedPR()
	s.PR.Comments = nil

	v := ComputeMergeGate(s)
	if v.NeedsDecision || v.ExitCode != ExitMergeBlocked {
		t.Errorf("ExitCode = %d, want %d — an unexplained missing review keeps the harsher code", v.ExitCode, ExitMergeBlocked)
	}
}

// The downgrade must not launder a real finding. A vacuous check next to
// unresolved review threads is still exit 3.
func TestVacuousCheckDoesNotLaunderACodeBlocker(t *testing.T) {
	s := reviewedPR()
	s.PR.HeadRefOid = "685efaf2fe6d6c1a4b7e1c9d5d2e3f4a5b6c7d8e"
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pass", Description: "Review rate limited"}}
	s.UnresolvedThreads = 2

	v := ComputeMergeGate(s)
	if v.NeedsDecision || v.ExitCode != ExitMergeBlocked {
		t.Errorf("ExitCode = %d, want %d — an unresolved finding is still work on the branch", v.ExitCode, ExitMergeBlocked)
	}
}

// The second half of robots-eowy: exit 4 told the caller to stop but not that
// stopping is permanent. CodeRabbit never re-reviews on its own when the
// window lapses — it needs a new push or an explicit `@coderabbitai review` —
// so "wait and re-run the gate" deadlocks forever. The verdict has to name the
// only action that can change it.
func TestNeedsDecisionNamesTheRecoveryAction(t *testing.T) {
	s := reviewedPR()
	s.PR.HeadRefOid = "685efaf2fe6d6c1a4b7e1c9d5d2e3f4a5b6c7d8e"
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pass", Description: "Review rate limited"}}

	notes := strings.Join(ComputeMergeGate(s).Notes, " ")
	for _, want := range []string{"@coderabbitai review", "does not re-review on its own", "Do NOT edit the branch"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes should contain %q so waiting does not read as the answer, got:\n%s", want, notes)
		}
	}
}

// The conservative direction: a blocker whose class nobody set must keep the
// harshest exit code. A forgotten class is not a downgrade.
func TestUnclassifiedBlockerKeepsTheHarshestExitCode(t *testing.T) {
	v := MergeGateVerdict{Blockers: []MergeBlocker{{Code: "mystery", Class: ""}}}
	if !hasUnclassifiedOrCode(v.Blockers) {
		t.Error("an unclassified blocker must be treated as a hard block on the code")
	}
}

// Every exit code the gate can return must be distinct and non-zero except
// ready — a caller that switches on them cannot tolerate a collision.
func TestMergeGateExitCodesAreDistinct(t *testing.T) {
	seen := map[int]string{
		config.ExitOK:          "ready",
		config.ExitRuntime:     "gh could not answer",
		config.ExitUsage:       "usage",
		ExitMergeBlocked:       "blocked",
		ExitMergeNeedsDecision: "needs-decision",
		ExitMergePending:       "pending",
	}
	if len(seen) != 6 {
		t.Errorf("exit codes collide: %v", seen)
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

// --- robots-g4qz: which repository is this verdict even about? -----------

func TestRepoFromRemoteURLHandlesEveryGitRemoteShape(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want string
	}{
		{"https://github.com/trillium/beads.git", "trillium/beads"},
		{"https://github.com/trillium/beads", "trillium/beads"},
		{"git@github.com:trillium/beads.git", "trillium/beads"},
		{"git@github.com:trillium/beads", "trillium/beads"},
		{"ssh://git@github.com/trillium/beads.git", "trillium/beads"},
		{"git://github.com/trillium/beads.git", "trillium/beads"},
		{"https://github.com/trillium/beads/", "trillium/beads"},
	} {
		got, ok := repoFromRemoteURL(tc.url)
		if !ok || got != tc.want {
			t.Errorf("repoFromRemoteURL(%q) = %q %v, want %q", tc.url, got, ok, tc.want)
		}
	}
	if _, ok := repoFromRemoteURL("not a url"); ok {
		t.Error("a non-URL should not resolve to a repo")
	}
	if _, ok := repoFromRemoteURL(""); ok {
		t.Error("an empty remote URL should not resolve to a repo")
	}
}

func TestRepoFromPRURL(t *testing.T) {
	got, ok := repoFromPRURL("https://github.com/gastownhall/gastown/pull/2")
	if !ok || got != "gastownhall/gastown" {
		t.Errorf("repoFromPRURL = %q %v, want gastownhall/gastown", got, ok)
	}
	if _, ok := repoFromPRURL("https://github.com/trillium/parlay"); ok {
		t.Error("a URL with no /pull/ segment is not a PR URL")
	}
}

// The gate must always say which repository it answered about. Without this,
// a misresolved repo produces a well-formed verdict about somebody else's PR
// and nothing in the output gives it away.
func TestVerdictAlwaysNamesTheRepositoryItAnsweredAbout(t *testing.T) {
	s := reviewedPR()
	s.Repo, s.RepoSource = "trillium/parlay", "origin remote"
	out := FormatMergeGate(s.PR, ComputeMergeGate(s))
	if !strings.Contains(out, "repo: trillium/parlay (from origin remote)") {
		t.Errorf("report must name the resolved repo and its source, got:\n%s", out)
	}
}

// The robots-g4qz fail-open: an upstream PR that landed months ago answers
// "already MERGED" (exit 0) for a fork PR that is still open and unreviewed.
// Even on that early-return path the repo has to be stated.
func TestMergedShortCircuitStillNamesTheRepository(t *testing.T) {
	s := reviewedPR()
	s.PR.State = "MERGED"
	s.Repo, s.RepoSource = "gastownhall/gastown", "gh default (no origin remote)"
	v := ComputeMergeGate(s)
	if !v.Merged {
		t.Fatalf("want merged verdict, got %+v", v)
	}
	out := FormatMergeGate(s.PR, v)
	if !strings.Contains(out, "repo: gastownhall/gastown") {
		t.Errorf("an exit-0 MERGED verdict must still name its repo, got:\n%s", out)
	}
}

func TestAnswerAboutADifferentRepositoryIsCalledOut(t *testing.T) {
	s := reviewedPR()
	s.Repo, s.RepoSource = "trillium/gastown", "origin remote"
	s.PR.URL = "https://github.com/gastownhall/gastown/pull/2"
	out := FormatMergeGate(s.PR, ComputeMergeGate(s))
	if !strings.Contains(out, "WARNING") || !strings.Contains(out, "gastownhall/gastown") {
		t.Errorf("a repo mismatch between request and answer must be called out, got:\n%s", out)
	}
}

func TestNoRepoMismatchWarningWhenTheyAgree(t *testing.T) {
	s := reviewedPR()
	s.Repo, s.RepoSource = "trillium/gastown", "origin remote"
	s.PR.URL = "https://github.com/trillium/gastown/pull/2"
	out := FormatMergeGate(s.PR, ComputeMergeGate(s))
	if strings.Contains(out, "WARNING") {
		t.Errorf("matching repos must not warn, got:\n%s", out)
	}
}

func TestResolveMergeGateRepoPrefersAnExplicitFlag(t *testing.T) {
	repo, src, err := resolveMergeGateRepo("trillium/beads")
	if err != nil || repo != "trillium/beads" || src != "--repo" {
		t.Errorf("resolveMergeGateRepo(explicit) = %q %q %v", repo, src, err)
	}
	if _, _, err := resolveMergeGateRepo("no-slash"); err == nil {
		t.Error("a malformed --repo must be a usage error, not a silent fallback")
	}
}

// The defect itself: in a clone with BOTH an origin and an upstream remote,
// gh's own base-repo resolution prefers `upstream`. The gate must pick
// origin — that is where the fleet's PR lives, and it is the same remote the
// mechanic contract's `git branch -r --contains` proof checks against.
func TestResolveMergeGateRepoPicksOriginOverUpstream(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("remote", "add", "origin", "https://github.com/trillium/beads.git")
	run("remote", "add", "upstream", "https://github.com/gastownhall/beads.git")

	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	repo, src, err := resolveMergeGateRepo("")
	if err != nil {
		t.Fatalf("resolveMergeGateRepo: %v", err)
	}
	if repo != "trillium/beads" {
		t.Errorf("resolved %q, want the origin fork trillium/beads — gh would have picked upstream", repo)
	}
	if src != "origin remote" {
		t.Errorf("source = %q, want %q", src, "origin remote")
	}
}

// --- head freshness (robots-bn5d) -----------------------------------------
//
// Every rule above is evaluated against ORIGIN's head, which is the commit a
// merge lands. The caller, though, has just authored a fix and pushed it, and
// reads READY as a verdict on that. On trillium/firstmate#91 the push had gone
// to the no-mistakes mirror and the pipeline had not yet reached origin, so
// READY meant "the PRE-fix commit is clean to merge" — and merging there would
// have dropped the fix for the very finding that had blocked the PR.

// localAhead returns a snapshot whose local branch holds `n` commits origin's
// PR head does not: the exact mid-push shape.
func localAhead(n int) MergeGateSnapshot {
	s := reviewedPR()
	s.Head = HeadFreshness{
		Known:     true,
		Branch:    s.PR.HeadRefName,
		LocalHead: "c2b8672a1f5f4b6b3c2a4f9d0e1a2b3c4d5e6f70",
		Relation:  RelationAhead,
		Ahead:     n,
	}
	return s
}

// The regression itself. Everything else about this PR is clean, so without
// the freshness check it is exit 0 READY.
func TestUnpushedLocalHeadIsPendingNotReady(t *testing.T) {
	v := ComputeMergeGate(localAhead(1))
	if v.Ready {
		t.Fatal("a PR whose fix has not reached origin must never be READY")
	}
	if !hasBlocker(v, "head-not-pushed") {
		t.Fatalf("want head-not-pushed, got %v", blockerCodes(v))
	}
	if !v.Pending || v.ExitCode != ExitMergePending {
		t.Errorf("want PENDING/exit %d, got pending=%v exit=%d", ExitMergePending, v.Pending, v.ExitCode)
	}
}

// The one-line reason has to name the commit count and the branch; a mechanic
// who reads only the blocker line still has to be able to act on it.
func TestUnpushedHeadBlockerNamesWhatWouldBeDropped(t *testing.T) {
	v := ComputeMergeGate(localAhead(2))
	out := FormatMergeGate(localAhead(2).PR, v)
	for _, want := range []string{"2 commit(s) ahead", "fix/robots-jap6-merge-gate", shortSHA(headSHA)} {
		if !strings.Contains(out, want) {
			t.Errorf("blocker must mention %q, got:\n%s", want, out)
		}
	}
}

// PENDING previously meant exactly one thing — the review is still running —
// and its notes say so. Printing that script over an unpushed head sends the
// mechanic to wait on a reviewer that has already answered.
func TestUnpushedHeadPendingNotesDoNotClaimAReviewIsRunning(t *testing.T) {
	v := ComputeMergeGate(localAhead(1))
	joined := strings.Join(v.Notes, "\n")
	if strings.Contains(joined, "still RUNNING") {
		t.Errorf("an unpushed head is not a running review, got notes:\n%s", joined)
	}
	if !strings.Contains(joined, "Do NOT merge") {
		t.Errorf("notes must forbid merging outright, got:\n%s", joined)
	}
}

// A running review and an unpushed head can be true at once, and each needs
// its own instruction — the review notes must not be suppressed by the head
// notes or vice versa.
func TestBothPendingShapesGetTheirOwnInstructions(t *testing.T) {
	s := localAhead(1)
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pending", Description: "Review in progress"}}
	v := ComputeMergeGate(s)
	joined := strings.Join(v.Notes, "\n")
	if !strings.Contains(joined, "Do NOT merge") || !strings.Contains(joined, "still RUNNING") {
		t.Errorf("both pending shapes must be explained, got:\n%s", joined)
	}
	if v.ExitCode != ExitMergePending {
		t.Errorf("exit = %d, want %d", v.ExitCode, ExitMergePending)
	}
}

// The downgrade must never launder a real finding: a failing check plus an
// unpushed head is still exit 3.
func TestOneCodeBlockerOutranksAnUnpushedHead(t *testing.T) {
	s := localAhead(1)
	s.Checks = []ghCheck{{Name: "build", Bucket: "fail", Description: "2 tests failed"}}
	v := ComputeMergeGate(s)
	if v.Pending || v.ExitCode != ExitMergeBlocked {
		t.Errorf("a failing check must keep exit %d, got pending=%v exit=%d", ExitMergeBlocked, v.Pending, v.ExitCode)
	}
}

// Diverged is worse than ahead — origin's head is not even an ancestor — so it
// blocks too, and says which shape it is.
func TestDivergedLocalHeadBlocksAndSaysSo(t *testing.T) {
	s := localAhead(1)
	s.Head.Relation = RelationDiverged
	v := ComputeMergeGate(s)
	if !hasBlocker(v, "head-not-pushed") {
		t.Fatalf("want head-not-pushed, got %v", blockerCodes(v))
	}
	if !strings.Contains(FormatMergeGate(s.PR, v), "DIVERGED") {
		t.Error("a divergence must be distinguished from a plain pending push")
	}
}

// A stale local checkout is normal and harmless: origin has commits this
// working copy has not fetched, and none of the local work is at risk.
func TestLocalBranchBehindOriginIsNotABlocker(t *testing.T) {
	s := reviewedPR()
	s.Head = HeadFreshness{Known: true, Branch: s.PR.HeadRefName, Relation: RelationBehind}
	v := ComputeMergeGate(s)
	if !v.Ready {
		t.Fatalf("being behind origin must not block a merge, got %v", blockerCodes(v))
	}
	if !strings.Contains(strings.Join(v.Notes, "\n"), "BEHIND") {
		t.Error("being behind should still be named, so it is not read as the ahead case")
	}
}

func TestLocalHeadMatchingOriginIsReadyAndSaysSo(t *testing.T) {
	s := reviewedPR()
	s.Head = HeadFreshness{Known: true, Branch: s.PR.HeadRefName, LocalHead: headSHA, Relation: RelationSame}
	v := ComputeMergeGate(s)
	if !v.Ready {
		t.Fatalf("want READY, got %v", blockerCodes(v))
	}
	if !strings.Contains(strings.Join(v.Notes, "\n"), "agrees") {
		t.Errorf("a verified-fresh head should say so, got notes %v", v.Notes)
	}
}

// "Could not tell" is not "they agree". The gate is often run somewhere with
// no copy of the branch, and there it must hand the caller the check it could
// not run rather than staying quiet.
func TestUnverifiableHeadFreshnessIsSaidOutLoud(t *testing.T) {
	s := reviewedPR() // Head zero value: Known=false
	v := ComputeMergeGate(s)
	if !v.Ready {
		t.Fatalf("an unverifiable head must not invent a blocker, got %v", blockerCodes(v))
	}
	joined := strings.Join(v.Notes, "\n")
	for _, want := range []string{"NOT verified", "headRefOid", shortSHA(headSHA)} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes must contain %q, got:\n%s", want, joined)
		}
	}
}

// On a MERGED PR there is no "before merging" left, so the same unverifiable
// freshness has to be phrased as doubt about what already landed.
func TestUnverifiableHeadFreshnessOnAMergedPRTalksAboutWhatLanded(t *testing.T) {
	s := reviewedPR()
	s.PR.State = "MERGED"
	v := ComputeMergeGate(s)
	joined := strings.Join(v.Notes, "\n")
	if strings.Contains(joined, "before merging") {
		t.Errorf("a merged PR must not be told to check something before merging, got:\n%s", joined)
	}
	for _, want := range []string{"NOT verified", "that MERGED", "branch -r --contains"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes must contain %q, got:\n%s", want, joined)
		}
	}
}

// The ticket's minimum ask: origin's head sha is visible on the READY line
// itself, because that is the line a mechanic acts on.
func TestReadyLineNamesTheCommitThatWouldMerge(t *testing.T) {
	s := reviewedPR()
	s.Head = HeadFreshness{Known: true, Branch: s.PR.HeadRefName, LocalHead: headSHA, Relation: RelationSame}
	out := FormatMergeGate(s.PR, ComputeMergeGate(s))
	if !strings.HasPrefix(out, "READY") || !strings.Contains(out, shortSHA(headSHA)) {
		t.Errorf("READY must name the commit it is about, got:\n%s", out)
	}
}

// Merging at the stale head is this defect already realized: the fix is gone
// and the merge looks like proof it landed. Waiting cannot fix that, so it is
// code-class, not pending.
func TestMergedPRWithUnpushedLocalHeadIsNotDone(t *testing.T) {
	s := localAhead(1)
	s.PR.State = "MERGED"
	s.Checks = nil
	v := ComputeMergeGate(s)
	if v.Ready {
		t.Fatal("a merge that dropped the local fix must not report ready")
	}
	if v.ExitCode != ExitMergeBlocked || v.Pending {
		t.Errorf("want exit %d and not pending, got exit=%d pending=%v", ExitMergeBlocked, v.ExitCode, v.Pending)
	}
	if !strings.Contains(FormatMergeGate(s.PR, v), "merged") {
		t.Error("the blocker should say the commits are not in what MERGED, past tense")
	}
}

// A merged PR whose local branch agrees keeps the old exit-0 short circuit.
func TestMergedPRWithNoLocalDriftStillShortCircuits(t *testing.T) {
	s := reviewedPR()
	s.PR.State = "MERGED"
	s.Checks = nil
	s.Head = HeadFreshness{Known: true, Branch: s.PR.HeadRefName, LocalHead: headSHA, Relation: RelationSame}
	v := ComputeMergeGate(s)
	if !v.Ready || !v.Merged || len(v.Blockers) != 0 {
		t.Errorf("want a clean merged verdict, got %+v", v)
	}
}

// --- detectHeadFreshness against a real git repository ---------------------

// gitRepoAt builds a throwaway repo with the given origin URL and returns a
// runner for further git commands in it.
func gitRepoAt(t *testing.T, origin string) (dir string, run func(args ...string) string) {
	t.Helper()
	dir = t.TempDir()
	run = func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "work")
	run("remote", "add", "origin", origin)
	run("commit", "-q", "--allow-empty", "-m", "base")
	return dir, run
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// The mid-push shape, measured for real: the branch has a commit origin's PR
// head does not.
func TestDetectHeadFreshnessSeesAnUnpushedCommit(t *testing.T) {
	dir, run := gitRepoAt(t, "https://github.com/trillium/parlay.git")
	originHead := run("rev-parse", "HEAD")
	run("commit", "-q", "--allow-empty", "-m", "the fix")
	chdir(t, dir)

	h := detectHeadFreshness("trillium/parlay", "work", originHead)
	if !h.Known {
		t.Fatalf("comparison should have been possible: %s", h.Reason)
	}
	if h.Relation != RelationAhead || h.Ahead != 1 {
		t.Errorf("relation=%q ahead=%d, want ahead/1", h.Relation, h.Ahead)
	}
}

func TestDetectHeadFreshnessSeesAgreement(t *testing.T) {
	dir, run := gitRepoAt(t, "git@github.com:trillium/parlay.git")
	head := run("rev-parse", "HEAD")
	chdir(t, dir)

	h := detectHeadFreshness("trillium/parlay", "work", head)
	if !h.Known || h.Relation != RelationSame || h.Ahead != 0 {
		t.Errorf("want a known/same/0 answer, got %+v", h)
	}
}

// A same-named branch in an unrelated checkout must never produce a blocker:
// the comparison is pinned to the repo the gate already resolved (robots-g4qz).
func TestDetectHeadFreshnessRefusesAForeignCheckout(t *testing.T) {
	dir, run := gitRepoAt(t, "https://github.com/trillium/beads.git")
	originHead := run("rev-parse", "HEAD")
	run("commit", "-q", "--allow-empty", "-m", "unrelated work")
	chdir(t, dir)

	h := detectHeadFreshness("trillium/parlay", "work", originHead)
	if h.Known {
		t.Fatalf("a different repository must not be compared, got %+v", h)
	}
	if !strings.Contains(h.Reason, "trillium/beads") {
		t.Errorf("the reason should name the mismatch, got %q", h.Reason)
	}
}

// Every unknown path carries a reason, so the gap is legible rather than
// silently reading as agreement.
func TestDetectHeadFreshnessExplainsEveryUnknown(t *testing.T) {
	dir, run := gitRepoAt(t, "https://github.com/trillium/parlay.git")
	head := run("rev-parse", "HEAD")
	chdir(t, dir)

	cases := []struct {
		name, repo, branch, sha, want string
	}{
		{"no branch name", "trillium/parlay", "", head, "head branch name"},
		{"no head sha", "trillium/parlay", "work", "", "head sha"},
		{"branch absent locally", "trillium/parlay", "not-here", head, "no local branch"},
		{"origin head unfetched", "trillium/parlay", "work", strings.Repeat("a", 40), "object store"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := detectHeadFreshness(tc.repo, tc.branch, tc.sha)
			if h.Known {
				t.Fatalf("expected an unknown answer, got %+v", h)
			}
			if !strings.Contains(h.Reason, tc.want) {
				t.Errorf("reason %q should mention %q", h.Reason, tc.want)
			}
		})
	}
}
