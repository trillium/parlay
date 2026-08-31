// Liveness lift unit 4: non-force removal + re-check-then-force fallback,
// stale marker, edge-triggered skip tracker.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/worktreeliveness"
)

func TestSweepSkipTrackerEdges(t *testing.T) {
	tr := newSweepSkipTracker()

	tr.beginPass()
	if !tr.shouldSurface("a", "hold: x") {
		t.Fatal("first pass must surface")
	}
	if !tr.shouldSurface("b", "hold: y") {
		t.Fatal("first pass must surface every id")
	}
	tr.endPass()

	tr.beginPass()
	if tr.shouldSurface("a", "hold: x") {
		t.Fatal("unchanged repeat must be suppressed")
	}
	if !tr.shouldSurface("b", "refused: z") {
		t.Fatal("a changed reason must surface")
	}
	tr.endPass()

	// "a" drops out for a pass, then returns: it must surface again.
	tr.beginPass()
	tr.endPass()
	tr.beginPass()
	if !tr.shouldSurface("a", "hold: x") {
		t.Fatal("an id that dropped out and returned must surface again")
	}
	tr.endPass()
}

func TestSweepSkipTrackerNilSurfacesEverything(t *testing.T) {
	var tr *sweepSkipTracker
	tr.beginPass() // must not panic
	for i := 0; i < 2; i++ {
		if !tr.shouldSurface("a", "hold: x") {
			t.Fatal("nil tracker must surface every line, every pass")
		}
	}
	tr.endPass()
}

func TestHasUncommittedIgnoresStaleMarkerOnly(t *testing.T) {
	repo := newLandedFixture(t)
	gitOut(t, repo, "checkout", "main")
	if hasUncommitted(repo) {
		t.Fatal("clean fixture reads uncommitted")
	}
	if err := os.WriteFile(filepath.Join(repo, worktreeStaleMarkerName), []byte("branch=x\nreason=y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if hasUncommitted(repo) {
		t.Fatal("the stale marker alone must not read as uncommitted work")
	}
	if err := os.WriteFile(filepath.Join(repo, "real-work.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasUncommitted(repo) {
		t.Fatal("real untracked work next to the marker must still refuse")
	}
}

func TestWriteWorktreeStaleMarker(t *testing.T) {
	repo := newLandedFixture(t) // on branch "feature"
	writeWorktreeStaleMarker(repo, "quarantine: too young")
	data, err := os.ReadFile(filepath.Join(repo, worktreeStaleMarkerName))
	if err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "branch=feature\n") || !strings.Contains(got, "reason=quarantine: too young\n") {
		t.Fatalf("marker = %q, want branch= and reason= lines", got)
	}
}

// teardownFixture builds a project repo with a pushed main, a linked worktree
// at $HOME-independent paths, and the agent store teardownAgentLive reads.
// The worktree's .git pointer is backdated past the quarantine.
func teardownFixture(t *testing.T) (agentID, project, wt string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	origin := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, origin, "init", "-q", "--bare", "-b", "main")

	project = t.TempDir()
	git(t, project, "init", "-q", "-b", "main")
	git(t, project, "config", "user.email", "t@t.t")
	git(t, project, "config", "user.name", "t")
	git(t, project, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, project, "remote", "add", "origin", origin)
	git(t, project, "push", "-q", "-u", "origin", "main")

	agentID = "td-fixture"
	wt = filepath.Join(t.TempDir(), "wt")
	git(t, project, "worktree", "add", "-q", wt, "-b", "agent/"+agentID)

	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(wt, ".git"), old, old); err != nil {
		t.Fatal(err)
	}

	store := filepath.Join(home, ".parlay", "agents", agentID)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: " + agentID + "\nworktree: " + wt + "\nproject: " + project + "\n---\n"
	if err := os.WriteFile(filepath.Join(store, "identity.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return agentID, project, wt
}

// A refusal must leave the reason in the tree for the next pass to read.
func TestTeardownRefusalWritesStaleMarker(t *testing.T) {
	stubLiveness(t, worktreeliveness.StateOf())
	stubBorrowIndex(t, nil, nil)
	agentID, _, wt := teardownFixture(t)
	if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := teardownAgentLive(agentID, false, nil); err == nil {
		t.Fatal("uncommitted work must refuse")
	}
	data, err := os.ReadFile(filepath.Join(wt, worktreeStaleMarkerName))
	if err != nil {
		t.Fatalf("refusal must write the stale marker: %v", err)
	}
	if !strings.Contains(string(data), "reason=") || !strings.Contains(string(data), "uncommitted changes") {
		t.Fatalf("marker = %q, want the refusal reason", string(data))
	}
}

// The full unit-4 removal chain: a tree carrying only the stale marker passes
// every gate (the marker is filtered), non-force removal fails on the
// untracked marker, the fresh re-check still passes, and the --force retry
// removes the tree. The pre-unit-4 success rate survives.
func TestTeardownNonForceRemovalFallsBackAfterRecheck(t *testing.T) {
	stubLiveness(t, worktreeliveness.StateOf())
	stubBorrowIndex(t, nil, nil)
	agentID, _, wt := teardownFixture(t)
	writeWorktreeStaleMarker(wt, "left by an earlier refused pass")

	msg, err := teardownAgentLive(agentID, false, nil)
	if err != nil {
		t.Fatalf("marker-only tree must tear down, got: %v", err)
	}
	if !strings.Contains(msg, "torn down") {
		t.Fatalf("msg = %q", msg)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree still present after fallback removal (err=%v)", err)
	}
}

// When non-force removal fails AND the fresh re-check no longer passes, the
// teardown returns the refusal instead of forcing: the re-check is what makes
// the --force retry safe, so its failure must stop the chain.
func TestTeardownRecheckFailureBlocksForceRetry(t *testing.T) {
	// The caller's cached probes say idle, so the first check passes; the
	// seam says unscanned, so the fresh re-check refuses — modeling a scan
	// that degraded (or a process that arrived) mid-teardown.
	stubLiveness(t, worktreeliveness.State{})
	stubBorrowIndex(t, nil, nil)
	agentID, _, wt := teardownFixture(t)
	writeWorktreeStaleMarker(wt, "makes non-force removal fail")
	idle := worktreeliveness.StateOf()
	probes := &teardownProbes{live: &idle}

	_, err := teardownAgentLive(agentID, false, probes)
	if err == nil || !strings.Contains(err.Error(), "liveness scan unavailable") {
		t.Fatalf("failed re-check must block the force retry, got: %v", err)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("blocked teardown destroyed the worktree anyway: %v", statErr)
	}
	// The re-check refusal is also recorded in the tree.
	if _, statErr := os.Stat(filepath.Join(wt, worktreeStaleMarkerName)); statErr != nil {
		t.Fatalf("re-check refusal must leave the marker: %v", statErr)
	}
}
