// Liveness lift unit 3: treehouse-lease gate, borrow-veto, stash check.
// Same seam discipline as teardown_gates_test.go — no real lsof, and the
// borrow scan runs against real directories only in the scanBorrowIndex tests
// (under a redirected $HOME).
package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/worktreeliveness"
)

func TestPreGitGateRefusesTreehousePoolSlot(t *testing.T) {
	// The lease gate is lexical and runs first, so no liveness or borrow
	// stubs: reaching either seam would itself be a bug here.
	slot := filepath.Join(t.TempDir(), ".treehouse", "parlay-abc123", "3", "parlay")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		err := checkWorktreePreGitSafety("parlay teardown", "a1", slot, force, nil)
		if err == nil {
			t.Fatalf("force=%v: treehouse pool slot must refuse, got nil", force)
		}
		if !strings.Contains(err.Error(), "treehouse pool slot") {
			t.Fatalf("force=%v: wrong refusal: %v", force, err)
		}
	}
}

func TestPreGitGateRefusesBorrowedWorktree(t *testing.T) {
	stubLiveness(t, worktreeliveness.StateOf())
	wt := agedWorktree(t)
	stubBorrowIndex(t, map[string][]borrowRef{
		worktreeliveness.NormalizePath(wt): {{id: "other-agent", state: "working"}},
	}, nil)
	for _, force := range []bool{false, true} {
		err := checkWorktreePreGitSafety("parlay teardown", "a1", wt, force, nil)
		if err == nil {
			t.Fatalf("force=%v: borrowed worktree must refuse, got nil", force)
		}
		if !strings.Contains(err.Error(), "other-agent") {
			t.Fatalf("force=%v: refusal must name the borrower: %v", force, err)
		}
	}
}

// An agent always points at its own worktree; that pointer must not veto its
// own teardown.
func TestPreGitGateBorrowIgnoresSelf(t *testing.T) {
	stubLiveness(t, worktreeliveness.StateOf())
	wt := agedWorktree(t)
	stubBorrowIndex(t, map[string][]borrowRef{
		worktreeliveness.NormalizePath(wt): {{id: "a1", state: "working"}},
	}, nil)
	if err := checkWorktreePreGitSafety("parlay teardown", "a1", wt, false, nil); err != nil {
		t.Fatalf("self-reference must not veto, got: %v", err)
	}
}

func TestPreGitGateRefusesOnBorrowScanError(t *testing.T) {
	stubLiveness(t, worktreeliveness.StateOf())
	stubBorrowIndex(t, nil, errors.New("permission denied on some identity.md"))
	wt := agedWorktree(t)
	for _, force := range []bool{false, true} {
		err := checkWorktreePreGitSafety("parlay teardown", "a1", wt, force, nil)
		if err == nil {
			t.Fatalf("force=%v: a failed borrow scan must protect the candidate, got nil", force)
		}
		if !strings.Contains(err.Error(), "borrow scan failed") {
			t.Fatalf("force=%v: wrong refusal: %v", force, err)
		}
	}
}

// borrowAgentStore writes one agent's identity.md (and optionally a status
// file) under $HOME/.parlay/agents — the exact shape scanBorrowIndex walks.
func borrowAgentStore(t *testing.T, home, id, worktree, statusLine string) {
	t.Helper()
	dir := filepath.Join(home, ".parlay", "agents", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: " + id + "\n"
	if worktree != "" {
		body += "worktree: " + worktree + "\n"
	}
	body += "---\n"
	if err := os.WriteFile(filepath.Join(dir, "identity.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if statusLine != "" {
		if err := os.WriteFile(filepath.Join(dir, "status"), []byte(statusLine+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanBorrowIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	w1 := t.TempDir()
	w2 := t.TempDir()

	borrowAgentStore(t, home, "busy", w1, "working: mid-task")
	borrowAgentStore(t, home, "finished", w1, "done: PR merged")
	borrowAgentStore(t, home, "crashed", w2, "failed: gave up")
	borrowAgentStore(t, home, "silent", w2, "") // no status file — can't prove finished
	borrowAgentStore(t, home, "no-worktree", "", "working: elsewhere")
	// A store dir with no identity.md at all is skipped, not an error.
	if err := os.MkdirAll(filepath.Join(home, ".parlay", "agents", "no-identity"), 0o755); err != nil {
		t.Fatal(err)
	}

	idx, err := scanBorrowIndex()
	if err != nil {
		t.Fatalf("scanBorrowIndex: %v", err)
	}
	refs1 := idx[worktreeliveness.NormalizePath(w1)]
	if len(refs1) != 1 || refs1[0].id != "busy" || refs1[0].state != "working" {
		t.Fatalf("w1 refs = %+v, want exactly the working borrower (done excluded)", refs1)
	}
	refs2 := idx[worktreeliveness.NormalizePath(w2)]
	if len(refs2) != 1 || refs2[0].id != "silent" || refs2[0].state != "absent" {
		t.Fatalf("w2 refs = %+v, want exactly the status-less borrower (failed excluded)", refs2)
	}
}

func TestScanBorrowIndexMissingAgentsDirIsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no .parlay/agents at all
	idx, err := scanBorrowIndex()
	if err != nil {
		t.Fatalf("a host with no agents dir has no borrowers, got error: %v", err)
	}
	if len(idx) != 0 {
		t.Fatalf("idx = %+v, want empty", idx)
	}
}

func TestScanBorrowIndexReadErrorAborts(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod 0 is not a read error for root")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	borrowAgentStore(t, home, "opaque", t.TempDir(), "working: x")
	if err := os.Chmod(filepath.Join(home, ".parlay", "agents", "opaque", "identity.md"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := scanBorrowIndex(); err == nil {
		t.Fatal("an unreadable identity.md must abort the scan (protecting every candidate), got nil")
	}
}

// stashFixture is a clean, pushed, aged repo with idle liveness and no
// borrowers — everything before the stash gate passes.
func stashFixture(t *testing.T) string {
	t.Helper()
	stubLiveness(t, worktreeliveness.StateOf())
	stubBorrowIndex(t, nil, nil)
	repo := newLandedFixture(t)
	gitOut(t, repo, "checkout", "main")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(repo, ".git"), old, old); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestGitSafetyRefusesStashedChanges(t *testing.T) {
	repo := stashFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("stashable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, repo, "stash", "push", "-m", "wip")
	// The stash rewrote .git's index (tmp file + rename), refreshing the
	// directory mtime the freshness gate reads — backdate it again.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(repo, ".git"), old, old); err != nil {
		t.Fatal(err)
	}

	err := checkWorktreeGitSafety("parlay teardown", "a1", repo, false)
	if err == nil || !strings.Contains(err.Error(), "stashed changes") {
		t.Fatalf("stashed work must refuse unforced, got: %v", err)
	}
	// --force bypasses the stash gate like the other git-state gates: stash
	// state is operator-inspectable.
	if err := checkWorktreeGitSafety("parlay teardown", "a1", repo, true); err != nil {
		t.Fatalf("force must bypass the stash gate, got: %v", err)
	}
}

// A worktree where `git stash list` cannot run at all (broken .git pointer)
// refuses rather than assuming no stashes — unlike hasUncommitted/hasUnpushed,
// this gate is new and owes no fidelity to the TS original's fail-open probes.
func TestGitSafetyRefusesUnreadableStashState(t *testing.T) {
	stubLiveness(t, worktreeliveness.StateOf())
	stubBorrowIndex(t, nil, nil)
	wt := agedWorktree(t) // .git points at "elsewhere"; every git probe fails

	err := checkWorktreeGitSafety("parlay teardown", "a1", wt, false)
	if err == nil || !strings.Contains(err.Error(), "stash state unreadable") {
		t.Fatalf("unreadable stash state must refuse unforced, got: %v", err)
	}
	if err := checkWorktreeGitSafety("parlay teardown", "a1", wt, true); err != nil {
		t.Fatalf("force accepts unreadable stash state with a warning, got: %v", err)
	}
}
