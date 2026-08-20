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
			BaseRefName:      "main",
			HeadRefOid:       headSHA,
			Author:           ghAuthor{Login: "trillium"},
			Comments: []ghComment{
				{Author: ghAuthor{Login: "coderabbitai"}, Body: realReviewBody(baseSHA, headSHA)},
			},
		},
		Checks:       []ghCheck{{Name: "CodeRabbit", State: "SUCCESS", Bucket: "pass", Description: "1 file reviewed"}},
		ThreadsKnown: true,
		// Up to date with main, and known to be — so the checks above are
		// statements about the merge that would actually happen.
		BehindKnown: true,
		BehindBy:    0,
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

// The robots-1hs5 regression: three PRs on trillium/firstmate (#76/#77/#79)
// each held a green check earned against an older main and, merged in turn,
// collectively broke main. GitHub recomputes refs/pull/N/merge when the base
// moves but never re-runs the check, so the green survives a base it no longer
// describes. Being behind is fixable on the branch, so it is code-class.
func TestBehindTheBaseBlocksEvenWithEverythingElseGreen(t *testing.T) {
	s := reviewedPR()
	s.BehindBy = 3
	v := ComputeMergeGate(s)
	if v.Ready {
		t.Fatalf("a PR behind its base must not be READY")
	}
	if !hasBlocker(v, "behind-base") {
		t.Fatalf("want behind-base, got %v", blockerCodes(v))
	}
	if v.ExitCode != ExitMergeBlocked {
		t.Errorf("ExitCode = %d, want %d (code-class: rebase fixes it)", v.ExitCode, ExitMergeBlocked)
	}
	if !strings.Contains(FormatMergeGate(s.PR, v), "main") {
		t.Errorf("the blocker should name the base branch, got %q", FormatMergeGate(s.PR, v))
	}
}

// mergeStateStatus=BEHIND is authoritative when GitHub bothers to report it,
// and it is the only signal left if the compare call failed. It is NOT
// sufficient on its own — see the next test for why.
func TestMergeStateStatusBehindBlocksWhenTheCompareCallFailed(t *testing.T) {
	s := reviewedPR()
	s.BehindKnown, s.BehindBy = false, 0
	s.PR.MergeStateStatus = "BEHIND"
	v := ComputeMergeGate(s)
	if !hasBlocker(v, "behind-base") || v.ExitCode != ExitMergeBlocked {
		t.Fatalf("want a code-class behind-base, got %v exit %d", blockerCodes(v), v.ExitCode)
	}
}

// Why the gate cannot rely on mergeStateStatus alone: GitHub only reports
// BEHIND when the base branch has protection requiring up-to-date branches.
// trillium/firstmate's main has no protection at all, so every behind PR there
// reports CLEAN (or UNSTABLE) — the exact repo this blocker was written for.
// The compare API's behind_by is true regardless of repo settings.
func TestBehindIsCaughtEvenWhenGitHubReportsCleanOnAnUnprotectedBase(t *testing.T) {
	s := reviewedPR()
	s.PR.MergeStateStatus = "CLEAN"
	s.BehindBy = 10
	if v := ComputeMergeGate(s); !hasBlocker(v, "behind-base") {
		t.Errorf("CLEAN must not launder a behind branch, got %v", blockerCodes(v))
	}
}

// One blocker, not two, when both signals agree.
func TestBehindIsReportedOnceWhenBothSignalsAgree(t *testing.T) {
	s := reviewedPR()
	s.BehindBy = 2
	s.PR.MergeStateStatus = "BEHIND"
	v := ComputeMergeGate(s)
	n := 0
	for _, c := range blockerCodes(v) {
		if c == "behind-base" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("behind-base reported %d times, want 1: %v", n, blockerCodes(v))
	}
}

// An unknown comparison is disclosed, not assumed current — same shape as an
// unreadable thread count. It must not block on its own: the compare call is
// best-effort and a network hiccup should not make the gate unusable.
func TestUnknownBaseComparisonIsNotedNotAssumedCurrent(t *testing.T) {
	s := reviewedPR()
	s.BehindKnown, s.BehindBy = false, 0
	v := ComputeMergeGate(s)
	if !v.Ready {
		t.Fatalf("an unknown comparison should not itself block, got %v", blockerCodes(v))
	}
	if !strings.Contains(strings.Join(v.Notes, " "), "UNKNOWN") {
		t.Errorf("want an UNKNOWN note, got %v", v.Notes)
	}
	// FormatMergeGate must not claim "green against the current base" when
	// freshness is unknown — that would contradict the UNKNOWN note above.
	out := FormatMergeGate(s.PR, v)
	if strings.Contains(out, "green against the current base") {
		t.Errorf("ready summary must not assert 'green against the current base' when BehindKnown=false, got:\n%s", out)
	}
	if !strings.Contains(out, "base freshness unknown") {
		t.Errorf("ready summary must qualify freshness when BehindKnown=false, got:\n%s", out)
	}
}

// A merged PR is past gating entirely — do not tell the captain to rebase
// something that already landed.
func TestBehindDoesNotFireOnAnAlreadyMergedPR(t *testing.T) {
	s := reviewedPR()
	s.PR.State = "MERGED"
	s.BehindBy = 5
	if v := ComputeMergeGate(s); !v.Ready || len(v.Blockers) != 0 {
		t.Errorf("a merged PR should short-circuit clean, got %v", blockerCodes(v))
	}
}

// behind-base is code-class, so it must survive the reviewer-unavailable
// downgrade exactly like a failing test does — a stale base is still a stale
// base while CodeRabbit is rate limited.
func TestBehindBaseOutranksReviewerUnavailability(t *testing.T) {
	s := reviewedPR()
	s.BehindBy = 1
	s.Checks = []ghCheck{{Name: "CodeRabbit", Bucket: "pass", Description: "Review rate limited"}}
	v := ComputeMergeGate(s)
	if v.NeedsDecision || v.ExitCode != ExitMergeBlocked {
		t.Errorf("want exit %d, got %d (needsDecision=%v) — a behind base is fixable on the branch",
			ExitMergeBlocked, v.ExitCode, v.NeedsDecision)
	}
}

func TestBehindByJSONDistinguishesUnknownFromZero(t *testing.T) {
	s := reviewedPR()
	if got := behindByJSON(s); got == nil || *got != 0 {
		t.Errorf("known-and-current should marshal as 0, got %v", got)
	}
	s.BehindKnown = false
	if got := behindByJSON(s); got != nil {
		t.Errorf("unknown should marshal as null, got %v", *got)
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
