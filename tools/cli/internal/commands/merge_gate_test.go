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
			HeadRefName:      "fix/robots-jap6-merge-gate",
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
	for _, want := range []string{"merge-and-disclose", "park", "needs-decision"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes should name %q so the caller has a bounded answer, got %v", want, v.Notes)
		}
	}
	// This used to assert the literal word "unbounded", from a version of the
	// notes that said "do NOT wait on this unbounded". That word was a proxy for
	// the real property — the caller must be told that polling alone never
	// clears this — and the notes now say it in different words, so asserting the
	// word would only pin prose. Assert the property instead, and additionally
	// require the re-request that the rewritten advice leads with (task-6ch1h):
	// a note that says "waiting will not help" without naming the action that
	// does help is the shape that sent six PRs to merge-and-disclose.
	if !strings.Contains(notes, "Waiting alone never clears this") {
		t.Errorf("notes must tell the caller that waiting alone never clears this, got %v", v.Notes)
	}
	if !strings.Contains(notes, "@coderabbitai review") {
		t.Errorf("notes must name the re-request, which is the action that does clear it, got %v", v.Notes)
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
	// "does not re-review on its own" was the original phrasing of the middle
	// claim. It is still exactly what the notes assert — only now they give the
	// stronger reason, which is that automatic review never fires for this repo
	// at all (fewer than 10 stars), so nobody is coming back rather than merely
	// not coming back promptly. Matching on the reason keeps this test pinned to
	// the claim rather than to a sentence.
	for _, want := range []string{"@coderabbitai review", "come back on their own", "Do NOT edit the branch"} {
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
		ExitMergeInfra:         "infra",
	}
	if len(seen) != 7 {
		t.Errorf("exit codes collide: %v", seen)
	}
}

// --- infra-failed checks (robots-6mw2) -------------------------------------
//
// The observed shape, from three trillium/firstmate runs in one afternoon: a
// GitHub Actions job dies during action setup, so the check is bucket=fail
// with an EMPTY description and annotations carrying only GitHub's own errors.
// No repo code ran at all.

// actionsLink is the check link shape gh reports for a GitHub Actions job —
// the only place the check-run id (and the run id for a re-run) is available.
const actionsLink = "https://github.com/trillium/firstmate/actions/runs/31119180717/job/92675866030"

// infraFailedCheck is a job that never got past action setup. Verbatim
// annotation text from check-run 92675866030.
func infraFailedCheck(name string) ghCheck {
	return ghCheck{
		Name: name, State: "FAILURE", Bucket: "fail", Link: actionsLink,
		AnnotationsKnown: true,
		Annotations: []ghAnnotation{
			{Level: "failure", Message: "Failed to resolve action download info."},
			{Level: "failure", Message: "Service Unavailable"},
		},
	}
}

// realFailedCheck is what a job that actually ran the repo's code and failed
// annotates — verbatim from check-run 92660828287, the genuine
// duplicate-doc-audience failure that shared a run with the infra ones.
func realFailedCheck(name string) ghCheck {
	return ghCheck{
		Name: name, State: "FAILURE", Bucket: "fail", Link: actionsLink,
		AnnotationsKnown: true,
		Annotations: []ghAnnotation{
			{Level: "warning", Message: "Node.js 20 is deprecated."},
			{Level: "failure", Message: "Process completed with exit code 1."},
		},
	}
}

// The whole point: red that never touched the diff must not read as "fix it on
// the branch". A mechanic given exit 3 here goes hunting a defect in code that
// never executed.
func TestInfraFailedCheckIsNotABlockOnTheCode(t *testing.T) {
	s := reviewedPR()
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pass", Description: "Review completed"},
		infraFailedCheck("Behavior tests (Herdr)"),
	}
	v := ComputeMergeGate(s)
	if v.ExitCode != ExitMergeInfra {
		t.Fatalf("want exit %d (infra), got %d — blockers %v", ExitMergeInfra, v.ExitCode, blockerCodes(v))
	}
	if !v.Infra || v.Ready {
		t.Errorf("want Infra and not ready, got infra=%v ready=%v", v.Infra, v.Ready)
	}
	if !hasBlocker(v, "check-did-not-run") {
		t.Errorf("want check-did-not-run, got %v", blockerCodes(v))
	}
	// Still a blocker: the code is unverified, just not accused.
	if len(v.Blockers) == 0 {
		t.Error("an infra failure must still block the merge")
	}
}

// A failure that DID run the code stays code-class, however GitHub-ish the
// job's other output looks.
func TestRealTestFailureStaysCodeClass(t *testing.T) {
	s := reviewedPR()
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pass", Description: "Review completed"},
		realFailedCheck("Behavior portable serial 3"),
	}
	v := ComputeMergeGate(s)
	if v.ExitCode != ExitMergeBlocked {
		t.Fatalf("want exit %d (blocked), got %d — blockers %v", ExitMergeBlocked, v.ExitCode, blockerCodes(v))
	}
	if !hasBlocker(v, "check-failed") {
		t.Errorf("want check-failed, got %v", blockerCodes(v))
	}
}

// The mixed run from the ticket: one job died in setup, another genuinely
// failed. A real finding must never be laundered into "GitHub's problem".
func TestOneRealFailureOutranksInfraFailures(t *testing.T) {
	s := reviewedPR()
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pass", Description: "Review completed"},
		infraFailedCheck("Test coverage guard"),
		realFailedCheck("Behavior portable serial 3"),
	}
	if v := ComputeMergeGate(s); v.ExitCode != ExitMergeBlocked {
		t.Fatalf("want exit %d (blocked), got %d — blockers %v", ExitMergeBlocked, v.ExitCode, blockerCodes(v))
	}
}

// A step that fails while asserting on a 503 prints "Service Unavailable" all
// over its log — but it still annotates the step's exit, which is the evidence
// that decides. The downgrade requires NO code-shaped annotation, not merely
// SOME infra-shaped one.
func TestInfraTextAlongsideARealFailureStaysCodeClass(t *testing.T) {
	s := reviewedPR()
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pass", Description: "Review completed"},
		{
			Name: "api tests", Bucket: "fail", Link: actionsLink, AnnotationsKnown: true,
			Annotations: []ghAnnotation{
				{Level: "failure", Message: "not ok 4 - retries on Service Unavailable"},
				{Level: "failure", Message: "Process completed with exit code 1."},
			},
		},
	}
	if v := ComputeMergeGate(s); v.ExitCode != ExitMergeBlocked {
		t.Fatalf("want exit %d (blocked), got %d — blockers %v", ExitMergeBlocked, v.ExitCode, blockerCodes(v))
	}
}

// Unreadable annotations are not evidence of innocence. Anything the gate
// could not positively identify as infra keeps the harsher code — the same
// fail-closed rule the rest of this file follows.
func TestUnreadableAnnotationsKeepAFailureCodeClass(t *testing.T) {
	s := reviewedPR()
	// AnnotationsKnown false: gh could not answer, or this is not an Actions
	// check at all (CodeRabbit's link is empty).
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pass", Description: "Review completed"},
		{Name: "Behavior tests (Herdr)", Bucket: "fail", Link: actionsLink},
	}
	if v := ComputeMergeGate(s); v.ExitCode != ExitMergeBlocked {
		t.Fatalf("want exit %d (blocked), got %d — blockers %v", ExitMergeBlocked, v.ExitCode, blockerCodes(v))
	}
	// Same for a failed job that reported no annotations at all.
	s.Checks[1] = ghCheck{Name: "Behavior tests (Herdr)", Bucket: "fail", Link: actionsLink, AnnotationsKnown: true}
	if v := ComputeMergeGate(s); v.ExitCode != ExitMergeBlocked {
		t.Fatalf("empty annotations: want exit %d, got %d", ExitMergeBlocked, v.ExitCode)
	}
}

// A cancelled job is an ending without a verdict — in the observed runs, the
// cascade half of the same incident (GitHub cancels the rest of a run whose
// siblings died in setup). It never reported on the code, so it is not a
// finding about it.
func TestCancelledCheckIsInfraNotACodeFinding(t *testing.T) {
	s := reviewedPR()
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pass", Description: "Review completed"},
		{Name: "Behavior portable parallel 2", State: "CANCELLED", Bucket: "cancel", Link: actionsLink},
	}
	v := ComputeMergeGate(s)
	if v.ExitCode != ExitMergeInfra {
		t.Fatalf("want exit %d (infra), got %d — blockers %v", ExitMergeInfra, v.ExitCode, blockerCodes(v))
	}
	if !hasBlocker(v, "check-did-not-run") {
		t.Errorf("want check-did-not-run, got %v", blockerCodes(v))
	}
}

// A cancelled job that DID manage to report a real failure first is a finding.
func TestCancelledCheckWithARealFailureStaysCodeClass(t *testing.T) {
	s := reviewedPR()
	c := realFailedCheck("Behavior portable parallel 2")
	c.Bucket, c.State = "cancel", "CANCELLED"
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pass", Description: "Review completed"},
		c,
	}
	if v := ComputeMergeGate(s); v.ExitCode != ExitMergeBlocked {
		t.Fatalf("want exit %d (blocked), got %d — blockers %v", ExitMergeBlocked, v.ExitCode, blockerCodes(v))
	}
}

// Pending outranks infra: `gh run rerun` refuses a run with jobs still in
// flight, so advising a re-run before the run finishes is advice that cannot
// be followed. Wait, re-run the gate, then act.
func TestPendingOutranksInfra(t *testing.T) {
	s := reviewedPR()
	s.PR.Comments = nil // no review evidence yet either
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pending", Description: "Review in progress"},
		infraFailedCheck("Behavior tests (Herdr)"),
	}
	if v := ComputeMergeGate(s); v.ExitCode != ExitMergePending {
		t.Fatalf("want exit %d (pending), got %d — blockers %v", ExitMergePending, v.ExitCode, blockerCodes(v))
	}
}

// Infra outranks reviewer-unavailable: re-running the jobs is a bounded step
// the mechanic can take alone, where needs-decision is terminal until the
// captain picks.
func TestInfraOutranksReviewerUnavailable(t *testing.T) {
	s := reviewedPR()
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pass", Description: "Review rate limited"},
		infraFailedCheck("Behavior tests (Herdr)"),
	}
	v := ComputeMergeGate(s)
	if v.ExitCode != ExitMergeInfra {
		t.Fatalf("want exit %d (infra), got %d — blockers %v", ExitMergeInfra, v.ExitCode, blockerCodes(v))
	}
	if v.NeedsDecision {
		t.Error("infra must not also claim needs-decision")
	}
}

// The verdict has to say what to do next, with the real run id, or the caller
// is left guessing which of two red-looking states it is in.
func TestInfraVerdictLeadsWithItsOwnHeaderAndNamesTheRerun(t *testing.T) {
	s := reviewedPR()
	s.Repo = "trillium/firstmate"
	s.Checks = []ghCheck{
		{Name: "CodeRabbit", Bucket: "pass", Description: "Review completed"},
		infraFailedCheck("Behavior tests (Herdr)"),
	}
	v := ComputeMergeGate(s)
	out := FormatMergeGate(s.PR, v)
	if !strings.HasPrefix(out, "INFRA (") {
		t.Errorf("want an INFRA header, got:\n%s", out)
	}
	for _, want := range []string{"gh run rerun 31119180717 --failed --repo trillium/firstmate", "WITHOUT EVALUATING THIS DIFF", "robots-8kkq"} {
		if !strings.Contains(out, want) {
			t.Errorf("infra notes must mention %q, got:\n%s", want, out)
		}
	}
}

// Unit-level coverage of the classifier's own contract, independent of how a
// whole verdict composes.
func TestClassifyFailedCheck(t *testing.T) {
	ann := func(level, msg string) ghAnnotation { return ghAnnotation{Level: level, Message: msg} }
	cases := []struct {
		name  string
		check ghCheck
		want  string
	}{
		{"infra only", infraFailedCheck("x"), ClassInfra},
		{"real failure", realFailedCheck("x"), ClassCode},
		{"warnings never decide", ghCheck{Bucket: "fail", AnnotationsKnown: true, Annotations: []ghAnnotation{
			ann("warning", "Failed to resolve action download info."),
			ann("failure", "Process completed with exit code 1."),
		}}, ClassCode},
		{"runner lost", ghCheck{Bucket: "fail", AnnotationsKnown: true, Annotations: []ghAnnotation{
			ann("failure", "The runner has received a shutdown signal."),
		}}, ClassInfra},
		{"unknown failure text", ghCheck{Bucket: "fail", AnnotationsKnown: true, Annotations: []ghAnnotation{
			ann("failure", "something nobody has seen before"),
		}}, ClassCode},
		{"cancelled, nothing known", ghCheck{Bucket: "cancel"}, ClassInfra},
		{"failed, nothing known", ghCheck{Bucket: "fail"}, ClassCode},
		// Verbatim from check-run 92675865968 during the incident: GitHub
		// never handed the job to a runner at all.
		{"runner never acquired", ghCheck{Bucket: "cancel", AnnotationsKnown: true, Annotations: []ghAnnotation{
			ann("failure", "The job was not acquired by Runner of type hosted even after multiple attempts"),
		}}, ClassInfra},
		// Verbatim from check-run 92675865937 in the same run — and
		// deliberately code-class: a hung test in the diff annotates exactly
		// this, so the gate must keep pointing at the branch.
		{"job timeout stays the branch's problem", ghCheck{Bucket: "cancel", AnnotationsKnown: true, Annotations: []ghAnnotation{
			ann("failure", "The job has exceeded the maximum execution time of 10m0s"),
		}}, ClassCode},
	}
	for _, tc := range cases {
		if got, _ := classifyFailedCheck(tc.check); got != tc.want {
			t.Errorf("%s: want class %q, got %q", tc.name, tc.want, got)
		}
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

// --- merged is not live (robots-oex0) ---------------------------------
//
// The mechanic contract proves a fix landed by showing origin/main contains
// the commit. That is only a proof of LIVENESS if origin/main is the artifact
// that runs. In pai-hooks it is not — ~/.claude/hooks symlinks into the
// checkout, so local main is live — and origin sat 20 commits behind it, so a
// merged commit satisfied "FIXED" without ever going live.

// laggingLive is the observed pai-hooks shape: origin/main 20 commits behind
// the branch the checkout actually runs, and 1 commit ahead of it (the squash
// of PR #5), i.e. genuinely diverged in both directions.
func laggingLive() LiveBranchState {
	return LiveBranchState{Known: true, Branch: "main", Ahead: 20, Behind: 1}
}

func notesJoined(v MergeGateVerdict) string { return strings.Join(v.Notes, "\n") }

// The drift must never change the exit code. Nothing about it says the diff is
// bad, and a gate that refuses every merge in a repo like pai-hooks gets
// ignored on the run that matters.
func TestOriginLaggingLiveIsNotedButNeverBlocks(t *testing.T) {
	s := reviewedPR()
	s.PR.BaseRefName = "main"
	s.Live = laggingLive()

	v := ComputeMergeGate(s)
	if !v.Ready || v.ExitCode != config.ExitOK {
		t.Fatalf("drift must not block: ready=%v exit=%d blockers %v", v.Ready, v.ExitCode, blockerCodes(v))
	}
	if !v.OriginLagsLive {
		t.Fatal("OriginLagsLive not set on a base branch 20 commits ahead of origin")
	}
	notes := notesJoined(v)
	if !strings.Contains(notes, "ORIGIN LAGS LIVE") {
		t.Errorf("notes never say the merge will not deploy:\n%s", notes)
	}
	if !strings.Contains(notes, "20 commit(s) ahead") {
		t.Errorf("notes omit how far origin lags:\n%s", notes)
	}
}

// The whole defect, at the surface a mechanic actually reads: "MERGED" is the
// word it converts into "FIXED", so a merged PR in a lagging repo must not be
// allowed to print that word unqualified.
func TestMergedPRInALaggingRepoSaysNotLiveInTheHeader(t *testing.T) {
	s := reviewedPR()
	s.PR.State = "MERGED"
	s.PR.BaseRefName = "main"
	s.Live = laggingLive()

	v := ComputeMergeGate(s)
	if !v.Merged || v.ExitCode != config.ExitOK {
		t.Fatalf("merged PR should still short-circuit to ready: %+v", v)
	}
	out := FormatMergeGate(s.PR, v)
	head := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(head, "NOT LIVE") {
		t.Errorf("header must qualify MERGED when origin lags live, got %q", head)
	}
	if !strings.Contains(notesJoined(v), "ORIGIN LAGS LIVE") {
		t.Errorf("merged short-circuit dropped the liveness note:\n%s", out)
	}
}

func TestReadyHeaderWarnsThatMergingWillNotDeploy(t *testing.T) {
	s := reviewedPR()
	s.PR.BaseRefName = "main"
	s.Live = laggingLive()

	head := strings.SplitN(FormatMergeGate(s.PR, ComputeMergeGate(s)), "\n", 2)[0]
	if !strings.Contains(head, "WILL NOT MAKE IT LIVE") {
		t.Errorf("bare READY hides the drift, got %q", head)
	}
}

// A repo in sync must stay silent — the note is only meaningful because it is
// rare, and READY has to keep meaning READY everywhere else.
func TestOriginInSyncSaysNothingAboutLiveness(t *testing.T) {
	s := reviewedPR()
	s.PR.BaseRefName = "main"
	s.Live = LiveBranchState{Known: true, Branch: "main"}

	v := ComputeMergeGate(s)
	if v.OriginLagsLive {
		t.Error("OriginLagsLive set on a branch that matches origin")
	}
	if strings.Contains(notesJoined(v), "ORIGIN LAGS LIVE") {
		t.Errorf("in-sync repo got a drift note:\n%s", notesJoined(v))
	}
	if head := strings.SplitN(FormatMergeGate(s.PR, v), "\n", 2)[0]; !strings.HasPrefix(head, "READY —") {
		t.Errorf("header = %q, want plain READY", head)
	}
}

// "Could not tell" is not "they agree". A checkout with no local base branch
// (or no git at all) must not have its silence read as an all-clear — but it
// also must not manufacture a warning it has no evidence for.
func TestUnmeasuredLiveStateIsSilent(t *testing.T) {
	s := reviewedPR()
	s.PR.BaseRefName = "main"
	s.Live = LiveBranchState{Branch: "main", Ahead: 20} // Known stays false

	v := ComputeMergeGate(s)
	if v.OriginLagsLive {
		t.Error("unmeasured drift reported as fact")
	}
	if strings.Contains(notesJoined(v), "ORIGIN LAGS LIVE") {
		t.Errorf("unmeasured state produced a note:\n%s", notesJoined(v))
	}
}

// Two-sided divergence cannot be fast-forwarded. Telling a mechanic to run
// `pull --ff-only` there hands them a command that is guaranteed to fail.
func TestDivergedBranchIsToldToMergeNotFastForward(t *testing.T) {
	s := reviewedPR()
	s.PR.BaseRefName = "main"
	s.Live = laggingLive() // Behind: 1

	notes := notesJoined(ComputeMergeGate(s))
	if !strings.Contains(notes, "git merge origin/main") {
		t.Errorf("diverged branch not told to merge:\n%s", notes)
	}
	if strings.Contains(notes, "--ff-only") {
		t.Errorf("suggested a fast-forward that cannot apply:\n%s", notes)
	}
}

func TestOriginOnlyBehindIsToldToFastForward(t *testing.T) {
	s := reviewedPR()
	s.PR.BaseRefName = "main"
	s.Live = LiveBranchState{Known: true, Branch: "main", Ahead: 3}

	notes := notesJoined(ComputeMergeGate(s))
	if !strings.Contains(notes, "--ff-only") {
		t.Errorf("a strictly-behind origin should fast-forward:\n%s", notes)
	}
}

// Same root cause, the symptom a mechanic hits first: a branch cut from the
// local base drags every unpushed commit into the PR, and GitHub calls the
// result CONFLICTING. Observed on pai-hooks#7, fixed by rebasing --onto.
func TestConflictingPRInALaggingRepoNamesTheRebase(t *testing.T) {
	s := reviewedPR()
	s.PR.BaseRefName = "main"
	s.PR.Mergeable = "CONFLICTING"
	s.Live = laggingLive()

	v := ComputeMergeGate(s)
	if !hasBlocker(v, "conflicting") {
		t.Fatalf("expected the conflicting blocker, got %v", blockerCodes(v))
	}
	if v.ExitCode != ExitMergeBlocked {
		t.Errorf("ExitCode = %d, want %d — a real conflict still blocks", v.ExitCode, ExitMergeBlocked)
	}
	if !strings.Contains(notesJoined(v), "git rebase --onto origin/main main") {
		t.Errorf("conflict left unexplained:\n%s", notesJoined(v))
	}
}

// A clean PR must not get the rebase advice — that note is an explanation of
// an observed CONFLICTING status, not a standing instruction.
func TestCleanPRInALaggingRepoIsNotToldToRebase(t *testing.T) {
	s := reviewedPR()
	s.PR.BaseRefName = "main"
	s.Live = laggingLive()

	if notes := notesJoined(ComputeMergeGate(s)); strings.Contains(notes, "git rebase --onto") {
		t.Errorf("clean PR told to rebase:\n%s", notes)
	}
}

// --- detectLiveBranchDrift over a real repository ---------------------

// gitFixture builds a throwaway repo and chdirs into it for the test.
func gitFixture(t *testing.T) func(args ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "gate@example.com")
	run("config", "user.name", "gate")

	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return run
}

// The measurement itself, against real refs: origin's copy and the local
// branch each carrying commits the other does not.
func TestDetectLiveBranchDriftCountsBothSides(t *testing.T) {
	run := gitFixture(t)
	run("commit", "-q", "--allow-empty", "-m", "base")
	run("branch", "mirror")
	run("commit", "-q", "--allow-empty", "-m", "local 1")
	run("commit", "-q", "--allow-empty", "-m", "local 2")
	run("checkout", "-q", "mirror")
	run("commit", "-q", "--allow-empty", "-m", "squash on origin")
	run("update-ref", "refs/remotes/origin/main", "mirror")
	run("checkout", "-q", "main")

	got := detectLiveBranchDrift("main")
	if !got.Known {
		t.Fatal("drift not measured in a repo where both refs exist")
	}
	if got.Ahead != 2 {
		t.Errorf("Ahead = %d, want 2 (commits origin does not have)", got.Ahead)
	}
	if got.Behind != 1 {
		t.Errorf("Behind = %d, want 1 (the squash only origin has)", got.Behind)
	}
}

func TestDetectLiveBranchDriftIsZeroWhenInSync(t *testing.T) {
	run := gitFixture(t)
	run("commit", "-q", "--allow-empty", "-m", "base")
	run("update-ref", "refs/remotes/origin/main", "main")

	got := detectLiveBranchDrift("main")
	if !got.Known || got.Ahead != 0 || got.Behind != 0 {
		t.Errorf("detectLiveBranchDrift = %+v, want known and zero on both sides", got)
	}
}

// No remote-tracking ref means nothing to compare against — unknowable, not
// "in sync". Reporting Known here would license the merged-means-fixed
// inference on exactly the checkouts that cannot support it.
func TestDetectLiveBranchDriftIsUnknownWithoutAnOriginRef(t *testing.T) {
	run := gitFixture(t)
	run("commit", "-q", "--allow-empty", "-m", "base")

	if got := detectLiveBranchDrift("main"); got.Known {
		t.Errorf("detectLiveBranchDrift = %+v, want Known=false with no origin/main", got)
	}
}

func TestDetectLiveBranchDriftIsUnknownForAnAbsentLocalBranch(t *testing.T) {
	run := gitFixture(t)
	run("commit", "-q", "--allow-empty", "-m", "base")
	run("update-ref", "refs/remotes/origin/release", "main")

	if got := detectLiveBranchDrift("release"); got.Known {
		t.Errorf("detectLiveBranchDrift = %+v, want Known=false with no local release branch", got)
	}
}

func TestDetectLiveBranchDriftIsUnknownWithoutABaseBranchName(t *testing.T) {
	if got := detectLiveBranchDrift(""); got.Known {
		t.Errorf("detectLiveBranchDrift(%q) = %+v, want Known=false", "", got)
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

// --- the re-request advice (task-6ch1h) -------------------------------------
//
// For a long time the exit-4 notes told the caller that re-requesting a review
// "has stayed limited across repeated attempts before" and to hand the choice
// to the captain without trying it. Six PRs (#116-#121) were merged unreviewed
// on that advice. It rested on a diagnosis nobody had checked: automatic review
// is OFF for this repo because it has fewer than 10 stars, and an explicit
// `@coderabbitai review` works — #122 came back "Review finished" in about a
// minute and found a real goroutine leak.
//
// These tests pin the advice, not the prose. Each asserts a property that, if
// it regressed, would put the fleet back to escalating before spending a
// comment that costs nothing.

// liveRateLimitedBody is the template as CodeRabbit posts it TODAY, captured
// verbatim from PR #123. It differs from rateLimitedBody above in wording
// ("included", no colon, no emphasis), which is exactly why the window pattern
// has to tolerate both spellings.
const liveRateLimitedBody = "<!-- This is an auto-generated comment: summarize by coderabbit.ai -->\n" +
	"<!-- This is an auto-generated comment: rate limited by coderabbit.ai -->\n\n" +
	"> [!WARNING]\n> ## Review limit reached\n>\n" +
	"> **Next included review available in 57 minutes.**\n>\n" +
	"> **Limit details:** You've used the included review currently available.\n"

func notesText(v MergeGateVerdict) string { return strings.Join(v.Notes, "\n") }

func blockerText(v MergeGateVerdict) string {
	parts := make([]string, 0, len(v.Blockers))
	for _, b := range v.Blockers {
		parts = append(parts, b.Detail)
	}
	return strings.Join(parts, "\n")
}

func rateLimitedSnapshot() MergeGateSnapshot {
	s := reviewedPR()
	s.Repo = "trillium/parlay"
	s.Checks = []ghCheck{{Name: "CodeRabbit", State: "SUCCESS", Bucket: "pass", Description: "Review rate limited"}}
	s.PR.Comments = []ghComment{{Author: ghAuthor{Login: "coderabbitai"}, Body: liveRateLimitedBody}}
	return s
}

// The core of task-6ch1h. Exit 4 means "a human should decide", and the gate
// must not reach for that until it has told the caller to spend the one
// reversible action that can change the answer.
func TestReviewerUnavailableTellsTheCallerToReRequestBeforeEscalating(t *testing.T) {
	v := ComputeMergeGate(rateLimitedSnapshot())
	if v.ExitCode != ExitMergeNeedsDecision {
		t.Fatalf("ExitCode = %d, want %d (blockers %v)", v.ExitCode, ExitMergeNeedsDecision, blockerCodes(v))
	}
	notes := notesText(v)
	if !strings.Contains(notes, "@coderabbitai review") {
		t.Errorf("the reviewer-unavailable notes never name the re-request that can clear this.\n"+
			"  Escalating without spending it is what merged six PRs unreviewed.\n  notes:\n%s", notes)
	}
	// Naming the command is not enough if the surrounding advice still says it
	// will not work. That sentence is the thing that actually did the damage.
	if strings.Contains(notes, "stayed limited across repeated attempts") {
		t.Errorf("the notes still carry the disproven claim that re-requesting does not work:\n%s", notes)
	}
}

// The escalation must be ordered AFTER the re-request, not offered alongside it
// as one of three equal options. A caller reading three peers picks whichever
// is cheapest for them, which is the escalation.
func TestReRequestIsAdvisedBeforeTheEscalationNotAlongsideIt(t *testing.T) {
	v := ComputeMergeGate(rateLimitedSnapshot())
	notes := notesText(v)
	req := strings.Index(notes, "@coderabbitai review")
	esc := strings.Index(notes, "needs-decision")
	if req < 0 || esc < 0 {
		t.Fatalf("expected both the re-request and the escalation in the notes:\n%s", notes)
	}
	if req > esc {
		t.Errorf("the escalation is advised before the re-request, so a caller reaches for the captain first.\n"+
			"  re-request at %d, escalation at %d:\n%s", req, esc, notes)
	}
}

// "Park until the reviewer returns" was never a terminating strategy for this
// repo: nobody is coming. The notes must say why rather than implying a wait.
func TestReviewerUnavailableNotesExplainWhyAutomaticReviewNeverFires(t *testing.T) {
	v := ComputeMergeGate(rateLimitedSnapshot())
	notes := notesText(v)
	if !strings.Contains(notes, "10 stars") {
		t.Errorf("the notes do not explain that automatic review is off for this repo, so\n"+
			"  'park and wait' still reads as a real option:\n%s", notes)
	}
}

// A quota that states its expiry is a wait. Throwing that number away is what
// forced the advice to say "after the window" without saying when.
func TestRateLimitedBlockerQuotesTheStatedWindow(t *testing.T) {
	v := ComputeMergeGate(rateLimitedSnapshot())
	if got := blockerText(v); !strings.Contains(got, "57 minutes") {
		t.Errorf("the rate-limit blocker drops the wait CodeRabbit stated in its own comment:\n%s", got)
	}
}

// The wording has already changed once. A pattern pinned to today's sentence
// would silently report "no window" against the older body, turning a wait back
// into an escalation.
func TestRateLimitWindowMatchesBothTemplatesCodeRabbitHasUsed(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"today's wording, plain", liveRateLimitedBody, "57 minutes"},
		{"older wording, colon and emphasis", rateLimitedWithReviewDetailsBody(baseSHA, headSHA), "51 minutes"},
		{"singular", "Next included review available in 1 minute.", "1 minute"},
		{"no window stated", rateLimitedBody, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rateLimitWindow([]string{tc.body}); got != tc.want {
				t.Errorf("rateLimitWindow = %q, want %q", got, tc.want)
			}
		})
	}
}

// Several bodies can carry a window once a PR has been re-requested more than
// once. Coming back too early spends a re-request that is refused; too late
// costs one gate re-run. Take the larger.
func TestRateLimitWindowTakesTheLongestStatedWait(t *testing.T) {
	got := rateLimitWindow([]string{
		"Next included review available in 12 minutes.",
		"Next included review available in 57 minutes.",
		"nothing here",
	})
	if got != "57 minutes" {
		t.Errorf("rateLimitWindow = %q, want the longest wait %q", got, "57 minutes")
	}
}

// AGENTS.md: gh prefers a remote named `upstream` over `origin`, so a bare
// `gh pr comment 122` on a fork posts to somebody else's repository. A gate
// that prints a command for a caller to paste has to print the safe spelling.
func TestRequestReviewCmdAlwaysPinsTheRepo(t *testing.T) {
	got := requestReviewCmd("trillium/parlay", 122)
	if !strings.Contains(got, "--repo trillium/parlay") {
		t.Errorf("requestReviewCmd = %q, must pass --repo so gh cannot resolve to an upstream fork", got)
	}
	if !strings.Contains(got, "@coderabbitai review") || !strings.Contains(got, "122") {
		t.Errorf("requestReviewCmd = %q, want the trigger body and the PR number", got)
	}
	// With no repo known, the flag must be omitted rather than emitted empty:
	// `--repo ''` is a hard gh error, which would be worse than the ambiguity.
	if bare := requestReviewCmd("", 122); strings.Contains(bare, "--repo") {
		t.Errorf("requestReviewCmd with no repo = %q, want no --repo flag at all", bare)
	}
}

// The advice change must not soften the verdict. Reviewer-unavailable is still
// non-zero, and a real code finding still outranks all of this.
func TestReRequestAdviceDoesNotMakeAnUnreviewedPRReady(t *testing.T) {
	v := ComputeMergeGate(rateLimitedSnapshot())
	if v.Ready || v.ExitCode == config.ExitOK {
		t.Fatalf("an unreviewed PR became ready once the notes changed: ready=%v exit=%d", v.Ready, v.ExitCode)
	}
}

func TestACodeBlockerStillOutranksTheReRequestAdvice(t *testing.T) {
	s := rateLimitedSnapshot()
	s.Checks = append(s.Checks, ghCheck{Name: "Go (build, vet, test, gofmt)", Bucket: "fail"})
	v := ComputeMergeGate(s)
	if v.ExitCode != ExitMergeBlocked {
		t.Errorf("ExitCode = %d, want %d — a failing check must outrank reviewer-unavailable (blockers %v)",
			v.ExitCode, ExitMergeBlocked, blockerCodes(v))
	}
}

// Found by using this verb on its own PRs: #123 was both rate-limited AND one
// commit behind main. Spending the re-request in that state buys a review
// pinned to a sha that is about to be replaced — the branch update turns it
// straight into `stale-review`, and the next review costs another full window.
// Order matters more than usual when the resource is one review per hour.
func TestReviewerUnavailableSaysToReachFinalHeadBeforeSpendingTheRequest(t *testing.T) {
	v := ComputeMergeGate(rateLimitedSnapshot())
	notes := notesText(v)
	if !strings.Contains(notes, "FINAL head") {
		t.Errorf("notes never warn to reach the final head before re-requesting, so a caller\n"+
			"  spends an hour-long quota on a sha they are about to replace:\n%s", notes)
	}
	// The warning is worthless after the fact — it has to precede the "now
	// escalate" step, same ordering rule as the re-request itself.
	if strings.Index(notes, "FINAL head") > strings.Index(notes, "needs-decision") {
		t.Errorf("the final-head warning comes after the escalation, too late to act on:\n%s", notes)
	}
}
