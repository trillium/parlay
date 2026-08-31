package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/worktreeliveness"
)

// stubLiveness pins the process-table probe for one test, the same way
// launch_test.go pins liveListeners. No real lsof runs anywhere below — CI is
// a Linux box where the real probe's answer is meaningless for these rules.
func stubLiveness(t *testing.T, s worktreeliveness.State) {
	t.Helper()
	prev := collectWorktreeLiveness
	collectWorktreeLiveness = func() worktreeliveness.State { return s }
	t.Cleanup(func() { collectWorktreeLiveness = prev })
}

// agedWorktree returns a directory containing a ".git" pointer whose mtime is
// backdated an hour, so the freshness quarantine sees a comfortably old tree.
func agedWorktree(t *testing.T) string {
	t.Helper()
	wt := t.TempDir()
	gitPtr := filepath.Join(wt, ".git")
	if err := os.WriteFile(gitPtr, []byte("gitdir: elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(gitPtr, old, old); err != nil {
		t.Fatal(err)
	}
	return wt
}

func TestPreGitGateRefusesOnUnscannedLiveness(t *testing.T) {
	stubLiveness(t, worktreeliveness.State{}) // Scanned=false
	wt := agedWorktree(t)
	for _, force := range []bool{false, true} {
		err := checkWorktreePreGitSafety("parlay teardown", "a1", wt, force, nil)
		if err == nil {
			t.Fatalf("force=%v: indeterminate scan must refuse, got nil", force)
		}
		if !strings.Contains(err.Error(), "liveness scan unavailable") {
			t.Fatalf("force=%v: wrong refusal: %v", force, err)
		}
	}
}

func TestPreGitGateRefusesLiveWorktreeEvenForced(t *testing.T) {
	wt := agedWorktree(t)
	sub := filepath.Join(wt, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	} // must exist: normalizePath resolves symlinks on both sides of the match
	stubLiveness(t, worktreeliveness.StateOf(worktreeliveness.Record{PID: "4242", Cwd: sub}))
	for _, force := range []bool{false, true} {
		err := checkWorktreePreGitSafety("parlay teardown", "a1", wt, force, nil)
		if err == nil {
			t.Fatalf("force=%v: live worktree must refuse, got nil", force)
		}
		if !strings.Contains(err.Error(), "4242") {
			t.Fatalf("force=%v: refusal must name the pid: %v", force, err)
		}
	}
}

func TestPreGitGateFreshnessQuarantine(t *testing.T) {
	stubLiveness(t, worktreeliveness.StateOf()) // scanned, nothing live
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	} // fresh mtime — just created

	if err := checkWorktreePreGitSafety("parlay teardown", "a1", wt, false, nil); err == nil {
		t.Fatal("young worktree must refuse unforced")
	} else if !strings.Contains(err.Error(), "quarantine") {
		t.Fatalf("wrong refusal: %v", err)
	}
	// --force bypasses freshness (and only freshness): pure impatience, the
	// operator can inspect the age, no data at risk.
	if err := checkWorktreePreGitSafety("parlay teardown", "a1", wt, true, nil); err != nil {
		t.Fatalf("force must bypass the quarantine, got: %v", err)
	}
}

func TestPreGitGateRefusesUnstatableAge(t *testing.T) {
	stubLiveness(t, worktreeliveness.StateOf())
	wt := t.TempDir() // no .git at all — age indeterminate
	if err := checkWorktreePreGitSafety("parlay teardown", "a1", wt, false, nil); err == nil {
		t.Fatal("un-stat-able .git must refuse unforced")
	} else if !strings.Contains(err.Error(), "indeterminate") {
		t.Fatalf("wrong refusal: %v", err)
	}
}

func TestPreGitGatePassesIdleAgedWorktree(t *testing.T) {
	stubLiveness(t, worktreeliveness.StateOf(worktreeliveness.Record{PID: "1", Cwd: "/somewhere/else"}))
	if err := checkWorktreePreGitSafety("parlay teardown", "a1", agedWorktree(t), false, nil); err != nil {
		t.Fatalf("idle aged worktree must pass, got: %v", err)
	}
}

func TestPreGitGateUsesCallerScanWithoutSelfServing(t *testing.T) {
	prev := collectWorktreeLiveness
	collectWorktreeLiveness = func() worktreeliveness.State {
		t.Fatal("seam consulted despite caller-supplied scan")
		return worktreeliveness.State{}
	}
	t.Cleanup(func() { collectWorktreeLiveness = prev })
	live := worktreeliveness.StateOf()
	if err := checkWorktreePreGitSafety("parlay teardown", "a1", agedWorktree(t), false, &live); err != nil {
		t.Fatalf("got: %v", err)
	}
}

func TestTeardownMinAgeOverride(t *testing.T) {
	stubLiveness(t, worktreeliveness.StateOf())
	state := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", state)
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	} // fresh — inside the default quarantine

	// 0 disables the quarantine.
	if err := os.WriteFile(filepath.Join(state, "teardown-min-age-minutes"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkWorktreePreGitSafety("parlay teardown", "a1", wt, false, nil); err != nil {
		t.Fatalf("minAge=0 must admit a fresh worktree, got: %v", err)
	}

	// Garbage keeps the default rather than silently disabling the gate.
	if err := os.WriteFile(filepath.Join(state, "teardown-min-age-minutes"), []byte("soon\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkWorktreePreGitSafety("parlay teardown", "a1", wt, false, nil); err == nil {
		t.Fatal("unparseable override must fall back to the default quarantine")
	}
}

// The gates run BEFORE the git probes: a tree that would also fail git checks
// refuses with the liveness message, proving the ordering — and proving that
// checkWorktreeGitSafety (the 4-arg wrapper both teardown and variant
// teardown call) routes through the new gates at all.
func TestGitSafetyRunsPreGitGatesFirst(t *testing.T) {
	stubLiveness(t, worktreeliveness.State{}) // indeterminate
	wt := t.TempDir()                         // not even a repo: git probes would misbehave
	err := checkWorktreeGitSafety("parlay teardown", "a1", wt, false)
	if err == nil || !strings.Contains(err.Error(), "liveness scan unavailable") {
		t.Fatalf("pre-git gate must fire before any git probe, got: %v", err)
	}
}

// Full chain: an idle, aged worktree whose git state is clean and pushed
// still tears down. The lift only ADDS refusals; the happy path survives.
func TestGitSafetyPassesCleanIdleAgedRepo(t *testing.T) {
	stubLiveness(t, worktreeliveness.StateOf())
	repo := newLandedFixture(t)
	gitOut(t, repo, "checkout", "main") // main is pushed; worktree clean
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(repo, ".git"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := checkWorktreeGitSafety("parlay teardown", "a1", repo, false); err != nil {
		t.Fatalf("clean idle aged repo must pass, got: %v", err)
	}
}
