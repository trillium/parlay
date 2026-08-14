package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The absence of these tests is why robots-ceon shipped: isContentLanded used
// two-arg `git merge-tree <branch> <head>`, whose git >= 2.38 output is a bare
// tree OID, and then asked whether that OID was empty or contained the branch
// name — neither of which can ever be true. The function returned false for
// every input, so teardown's landed-work escape hatch had never once fired and
// nothing noticed. Every case below runs against a real on-disk git repo with a
// real bare "origin", because the defect lived entirely in what git actually
// prints.

// gitOut is guard_test.go's git() plus the trimmed output — these tests need
// commit/tree OIDs back, not just a pass/fail.
func gitOut(t *testing.T, dir string, argv ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, argv...)...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(argv, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newLandedFixture builds a repo with a bare origin, one base commit pushed to
// origin/main, and a `feature` branch (checked out) carrying one unpushed
// commit that adds feature.txt. It returns the repo path.
func newLandedFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "repo")

	if out, err := exec.Command("git", "init", "--bare", "-b", "main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "init", "-b", "main", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	gitOut(t, repo, "config", "user.email", "test@example.com")
	gitOut(t, repo, "config", "user.name", "Test")
	gitOut(t, repo, "config", "commit.gpgsign", "false")

	writeAt(t, repo, "base.txt", "base\n")
	gitOut(t, repo, "add", "-A")
	gitOut(t, repo, "commit", "-m", "base")
	gitOut(t, repo, "remote", "add", "origin", origin)
	gitOut(t, repo, "push", "-u", "origin", "main")
	// symbolic-ref refs/remotes/origin/HEAD is what isContentLanded reads to
	// learn the default branch; a plain clone gets it for free, `git init` +
	// `git remote add` does not.
	gitOut(t, repo, "remote", "set-head", "origin", "main")

	gitOut(t, repo, "checkout", "-b", "feature")
	writeAt(t, repo, "feature.txt", "feature work\n")
	gitOut(t, repo, "add", "-A")
	gitOut(t, repo, "commit", "-m", "feature work")

	return repo
}

func writeAt(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func headOID(t *testing.T, repo string) string {
	t.Helper()
	return gitOut(t, repo, "rev-parse", "HEAD")
}

// The regression the ticket names: content that genuinely landed upstream as a
// SQUASH commit is unreachable from any remote ref, so hasUnpushed is true —
// isContentLanded is the only thing standing between that agent and a refused
// teardown. It must say true.
func TestIsContentLandedTrueWhenContentSquashMergedUpstream(t *testing.T) {
	repo := newLandedFixture(t)
	featureHead := headOID(t, repo)

	// Land the same content on main as a squash (new commit, unrelated OID),
	// push it, and go back to the feature branch.
	gitOut(t, repo, "checkout", "main")
	gitOut(t, repo, "merge", "--squash", "feature")
	gitOut(t, repo, "commit", "-m", "squashed feature work")
	gitOut(t, repo, "push", "origin", "main")
	gitOut(t, repo, "checkout", "feature")

	if !hasUnpushed(repo) {
		t.Fatal("fixture is wrong: the squash-merged feature commit should still be unpushed")
	}
	if !isContentLanded(repo, featureHead) {
		t.Error("isContentLanded = false for content already squash-merged into origin/main; want true")
	}
}

// A merge commit (not a squash) is the easier half of the same claim.
func TestIsContentLandedTrueWhenContentMergedUpstream(t *testing.T) {
	repo := newLandedFixture(t)
	featureHead := headOID(t, repo)

	gitOut(t, repo, "checkout", "main")
	gitOut(t, repo, "merge", "--no-ff", "-m", "merge feature", "feature")
	gitOut(t, repo, "push", "origin", "main")
	gitOut(t, repo, "checkout", "feature")

	if !isContentLanded(repo, featureHead) {
		t.Error("isContentLanded = false for content merged into origin/main; want true")
	}
}

// The safety direction: genuinely unlanded work must NOT read as landed, or
// teardown deletes it.
func TestIsContentLandedFalseForUnlandedWork(t *testing.T) {
	repo := newLandedFixture(t)
	if isContentLanded(repo, headOID(t, repo)) {
		t.Error("isContentLanded = true for work never merged anywhere; want false")
	}
}

// Content landed upstream AFTER this worktree last synced: origin/main is stale
// locally, so the answer is only correct if isContentLanded refreshes the
// remote-tracking ref itself. Simulated by pushing from a second clone.
func TestIsContentLandedFetchesBeforeComparing(t *testing.T) {
	repo := newLandedFixture(t)
	featureHead := headOID(t, repo)
	originURL := gitOut(t, repo, "remote", "get-url", "origin")

	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("git", "clone", originURL, other).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	gitOut(t, other, "config", "user.email", "test@example.com")
	gitOut(t, other, "config", "user.name", "Test")
	writeAt(t, other, "feature.txt", "feature work\n")
	gitOut(t, other, "add", "-A")
	gitOut(t, other, "commit", "-m", "same content, landed by someone else")
	gitOut(t, other, "push", "origin", "main")

	// repo has never fetched, so its origin/main still points at the base commit.
	if !isContentLanded(repo, featureHead) {
		t.Error("isContentLanded = false with a stale origin/main; want true (it must fetch first)")
	}
}

// Inconclusive must mean refuse. Without refs/remotes/origin/HEAD there is no
// default branch to compare against.
func TestIsContentLandedFalseWithoutOriginHEAD(t *testing.T) {
	repo := newLandedFixture(t)
	featureHead := headOID(t, repo)
	gitOut(t, repo, "checkout", "main")
	gitOut(t, repo, "merge", "--squash", "feature")
	gitOut(t, repo, "commit", "-m", "squashed")
	gitOut(t, repo, "push", "origin", "main")
	gitOut(t, repo, "checkout", "feature")

	gitOut(t, repo, "remote", "set-head", "origin", "--delete")

	if isContentLanded(repo, featureHead) {
		t.Error("isContentLanded = true with no refs/remotes/origin/HEAD; want false (refuse when inconclusive)")
	}
}

// A conflicting merge is inconclusive too — `merge-tree --write-tree` exits
// non-zero and prints a conflict report rather than a tree OID.
func TestIsContentLandedFalseOnMergeConflict(t *testing.T) {
	repo := newLandedFixture(t)

	// Both sides change base.txt differently.
	writeAt(t, repo, "base.txt", "feature edit\n")
	gitOut(t, repo, "add", "-A")
	gitOut(t, repo, "commit", "-m", "feature edits base")
	featureHead := headOID(t, repo)

	gitOut(t, repo, "checkout", "main")
	writeAt(t, repo, "base.txt", "main edit\n")
	gitOut(t, repo, "add", "-A")
	gitOut(t, repo, "commit", "-m", "main edits base")
	gitOut(t, repo, "push", "origin", "main")
	gitOut(t, repo, "checkout", "feature")

	if isContentLanded(repo, featureHead) {
		t.Error("isContentLanded = true on a conflicting merge; want false (refuse when inconclusive)")
	}
}

// The help text must not promise a strategy the code does not have — the second
// half of robots-ceon. Two-thirds of `teardown --help` was fiction: a PR
// patch-id path that exists in neither CLI.
func TestTeardownHelpDoesNotPromisePatchID(t *testing.T) {
	out := captureStdout(t, func() { helpWanted("teardown", []string{"--help"}) })
	if strings.Contains(out, "via PR patch-id") {
		t.Errorf("teardown --help still advertises a patch-id strategy that is not implemented:\n%s", out)
	}
	if !strings.Contains(out, "No PR/patch-id check exists") {
		t.Errorf("teardown --help should say outright that no patch-id check exists:\n%s", out)
	}
	if !strings.Contains(out, "merge-tree") {
		t.Errorf("teardown --help should still describe the merge-tree check it does implement:\n%s", out)
	}
}
