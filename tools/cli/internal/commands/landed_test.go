package commands

import (
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// landedSnap builds a snapshot that PASSES every assertion, so each test can
// break exactly one thing and prove that one thing is what the proof caught.
func landedSnap() LandedSnapshot {
	return LandedSnapshot{
		PR: ghLandedPR{
			Number:      14,
			URL:         "https://github.com/trillium/no-mistakes/pull/14",
			State:       "MERGED",
			MergedAt:    "2026-08-06T12:00:00Z",
			MergeCommit: ghMergeCommit{OID: "1111111111111111111111111111111111111111"},
			HeadRefOid:  "2222222222222222222222222222222222222222",
			BaseRefName: "main",
		},
		Repo:           "trillium/no-mistakes",
		RepoSource:     "origin remote",
		Remote:         "origin",
		RemoteRepo:     "trillium/no-mistakes",
		InRepo:         true,
		Branch:         "origin/main",
		BranchSource:   "PR base branch",
		ProofSHA:       "1111111111111111111111111111111111111111",
		ProofSHAKind:   "merge commit",
		ContainingRefs: []string{"origin/main", "origin/fix/thing"},
		ContainsKnown:  true,
	}
}

func hasLandedBlocker(v LandedVerdict, code string) bool {
	for _, b := range v.Blockers {
		if b.Code == code {
			return true
		}
	}
	return false
}

func TestLandedAcceptsAGenuinelyLandedFix(t *testing.T) {
	v := ComputeLanded(landedSnap())
	if !v.Landed || v.ExitCode != config.ExitOK {
		t.Fatalf("want landed/exit 0, got landed=%v exit=%d blockers=%+v", v.Landed, v.ExitCode, v.Blockers)
	}
}

// The robots-0a77 defect itself, in the shape it was observed: gh answered
// MERGED for the UPSTREAM project's PR #14 while trillium/no-mistakes#14 was
// still open. The proof must refuse, not congratulate.
func TestLandedRefusesWhenGitHubAnsweredForADifferentRepo(t *testing.T) {
	s := landedSnap()
	s.PR.URL = "https://github.com/kunchenguid/no-mistakes/pull/14"
	v := ComputeLanded(s)
	if v.Landed {
		t.Fatal("a MERGED verdict about a different repository must not count as landed")
	}
	if !hasLandedBlocker(v, "repo-mismatch") {
		t.Fatalf("want repo-mismatch, got %+v", v.Blockers)
	}
	if v.ExitCode != ExitNotLanded {
		t.Fatalf("want exit %d, got %d", ExitNotLanded, v.ExitCode)
	}
}

// The other half of the same defect: the local containment check only means
// something if this checkout actually pushes to the repository gh was asked
// about. With no such remote the two halves describe different repositories.
func TestLandedRefusesWhenNoRemotePointsAtTheRepo(t *testing.T) {
	s := landedSnap()
	s.Remote, s.RemoteRepo = "", ""
	v := ComputeLanded(s)
	if v.Landed || !hasLandedBlocker(v, "no-remote-for-repo") {
		t.Fatalf("want no-remote-for-repo refusal, got landed=%v %+v", v.Landed, v.Blockers)
	}
}

func TestLandedRefusesOutsideAGitCheckout(t *testing.T) {
	s := landedSnap()
	s.InRepo = false
	v := ComputeLanded(s)
	if v.Landed || !hasLandedBlocker(v, "no-git-checkout") {
		t.Fatalf("want no-git-checkout refusal, got landed=%v %+v", v.Landed, v.Blockers)
	}
}

func TestLandedRefusesARemoteThatPointsSomewhereElse(t *testing.T) {
	s := landedSnap()
	s.RemoteRepo = "kunchenguid/no-mistakes"
	v := ComputeLanded(s)
	if v.Landed || !hasLandedBlocker(v, "remote-mismatch") {
		t.Fatalf("want remote-mismatch refusal, got landed=%v %+v", v.Landed, v.Blockers)
	}
}

func TestLandedRefusesAnOpenPR(t *testing.T) {
	s := landedSnap()
	s.PR.State = "OPEN"
	v := ComputeLanded(s)
	if v.Landed || !hasLandedBlocker(v, "pr-not-merged") {
		t.Fatalf("want pr-not-merged refusal, got landed=%v %+v", v.Landed, v.Blockers)
	}
}

// The gh half alone is not the proof: a PR can read MERGED while the commit is
// not on the branch this checkout calls the base (the fork collision again,
// and also a merge into some other branch).
func TestLandedRefusesMergedButNotOnTheBaseBranch(t *testing.T) {
	s := landedSnap()
	s.ContainingRefs = []string{"origin/fix/thing"}
	v := ComputeLanded(s)
	if v.Landed || !hasLandedBlocker(v, "not-on-branch") {
		t.Fatalf("want not-on-branch refusal, got landed=%v %+v", v.Landed, v.Blockers)
	}
	if !strings.Contains(v.Blockers[0].Detail, "origin/fix/thing") {
		t.Fatalf("the refusal should say what the commit IS on, got %q", v.Blockers[0].Detail)
	}
}

// "git could not answer" is not "the commit is absent". Reading an unanswered
// containment check either way would be a guess; unproven is not proven.
func TestLandedRefusesWhenContainmentCouldNotBeChecked(t *testing.T) {
	s := landedSnap()
	s.ContainsKnown, s.ContainingRefs, s.ContainsErr = false, nil, "malformed object name"
	v := ComputeLanded(s)
	if v.Landed || !hasLandedBlocker(v, "containment-unknown") {
		t.Fatalf("want containment-unknown refusal, got landed=%v %+v", v.Landed, v.Blockers)
	}
}

func TestLandedRefusesWithNoCommitToProve(t *testing.T) {
	s := landedSnap()
	s.ProofSHA, s.ProofSHAKind = "", ""
	v := ComputeLanded(s)
	if v.Landed || !hasLandedBlocker(v, "no-commit") {
		t.Fatalf("want no-commit refusal, got landed=%v %+v", v.Landed, v.Blockers)
	}
}

func TestLandedRefusesWithNoBaseBranch(t *testing.T) {
	s := landedSnap()
	s.Branch, s.BranchSource = "", ""
	v := ComputeLanded(s)
	if v.Landed || !hasLandedBlocker(v, "no-base-branch") {
		t.Fatalf("want no-base-branch refusal, got landed=%v %+v", v.Landed, v.Blockers)
	}
}

// Every verdict must name the repository it answered about — the whole defect
// was an answer that read perfectly while describing a different repository.
func TestLandedAlwaysReportsWhichRepoItAnsweredAbout(t *testing.T) {
	v := ComputeLanded(landedSnap())
	joined := strings.Join(v.Notes, "\n")
	if !strings.Contains(joined, "repo: trillium/no-mistakes (from origin remote)") {
		t.Fatalf("verdict must name the repo and how it was chosen, got %q", joined)
	}
	if !strings.Contains(joined, "origin/main") {
		t.Fatalf("verdict must name the branch the proof was against, got %q", joined)
	}
}

// Failures are reported all at once, so a mechanic sees the whole picture
// rather than one blocker per re-run.
func TestLandedReportsEveryFailureAtOnce(t *testing.T) {
	s := landedSnap()
	s.PR.State = "OPEN"
	s.ContainingRefs = []string{"origin/fix/thing"}
	v := ComputeLanded(s)
	if !hasLandedBlocker(v, "pr-not-merged") || !hasLandedBlocker(v, "not-on-branch") {
		t.Fatalf("want both blockers, got %+v", v.Blockers)
	}
}

func TestLandedNotesTellTheMechanicNotToClaimFixed(t *testing.T) {
	s := landedSnap()
	s.PR.State = "OPEN"
	v := ComputeLanded(s)
	joined := strings.Join(v.Notes, "\n")
	if !strings.Contains(joined, "do not claim FIXED") {
		t.Fatalf("a failed proof must say not to claim FIXED, got %q", joined)
	}
}

// for-each-ref lists the symbolic <remote>/HEAD alongside real branches; it
// points at another ref rather than being somewhere a commit lands.
func TestParseRefLinesDropsSymbolicHeadAndBlanks(t *testing.T) {
	got := parseRefLines("origin/HEAD\norigin/main\n\norigin/fix/thing\n")
	want := []string{"origin/main", "origin/fix/thing"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
}

// `--branch main` and `--branch origin/main` are the same request; the branch
// is normalized to the resolved remote before comparison, so containsRef only
// ever sees fully-qualified refs — but a bare name must still match if one
// reaches it.
func TestContainsRefIsExactAndCaseInsensitive(t *testing.T) {
	refs := []string{"origin/main"}
	if !containsRef(refs, "ORIGIN/MAIN") {
		t.Fatal("ref comparison should be case-insensitive")
	}
	if containsRef(refs, "main") {
		t.Fatal("a bare branch name must not match a remote-tracking ref of a different shape")
	}
	if containsRef(refs, "origin/main-2") {
		t.Fatal("ref comparison must be exact, not a prefix match")
	}
}

func TestFormatLandedLeadsWithTheVerdict(t *testing.T) {
	s := landedSnap()
	out := FormatLanded(s.PR, ComputeLanded(s))
	if !strings.HasPrefix(out, "LANDED — https://github.com/trillium/no-mistakes/pull/14") {
		t.Fatalf("unexpected header: %q", out)
	}
	s.PR.State = "CLOSED"
	out = FormatLanded(s.PR, ComputeLanded(s))
	if !strings.HasPrefix(out, "NOT-LANDED (1)") {
		t.Fatalf("unexpected header: %q", out)
	}
}
