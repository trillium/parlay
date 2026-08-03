// Runtime worktree-tangle guard tests — ported from
// packages/cli/src/commands-guard.test.ts (ticket B4). Exercises real git
// repos in a temp dir: default-branch resolution, the tangle predicate on
// every state, and the banner text/restore command.
package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// captureStderr runs fn with os.Stderr redirected to an in-memory pipe and
// returns everything it wrote. No existing helper in this package swaps
// os.Stderr (only captureStdout, in testhelpers_test.go) — guard's banners
// go to stderr, so this is a new sibling of that helper.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 4096)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if err != nil {
				break
			}
		}
		done <- string(buf)
	}()

	fn()

	w.Close()
	os.Stderr = orig
	return <-done
}

func setupGuardRepo(t *testing.T) string {
	t.Helper()
	repo, err := os.MkdirTemp("", "parlay-guard-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(repo) })
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.email", "t@t.t")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	return repo
}

// ── defaultBranch ────────────────────────────────────────────────────────

func TestDefaultBranchResolvesLocalMain(t *testing.T) {
	repo := setupGuardRepo(t)
	if got := defaultBranch(repo); got != "main" {
		t.Errorf("defaultBranch(repo) = %q, want %q", got, "main")
	}
}

func TestDefaultBranchNonGitDir(t *testing.T) {
	nd, err := os.MkdirTemp("", "parlay-guard-nogit-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(nd)
	if got := defaultBranch(nd); got != "" {
		t.Errorf("defaultBranch(nonGitDir) = %q, want \"\"", got)
	}
}

// ── primaryTangleBranch ──────────────────────────────────────────────────

func TestPrimaryTangleBranchOnDefaultBranch(t *testing.T) {
	repo := setupGuardRepo(t)
	git(t, repo, "checkout", "-q", "main")
	if got := primaryTangleBranch(repo); got != "" {
		t.Errorf("primaryTangleBranch(repo on main) = %q, want \"\"", got)
	}
}

func TestPrimaryTangleBranchOnNamedFeatureBranch(t *testing.T) {
	repo := setupGuardRepo(t)
	git(t, repo, "checkout", "-q", "-b", "parlay-variant/foo-wt1")
	if got := primaryTangleBranch(repo); got != "parlay-variant/foo-wt1" {
		t.Errorf("primaryTangleBranch(repo) = %q, want %q", got, "parlay-variant/foo-wt1")
	}
	git(t, repo, "checkout", "-q", "main")
}

func TestPrimaryTangleBranchDetachedHead(t *testing.T) {
	repo := setupGuardRepo(t)
	git(t, repo, "checkout", "-q", "--detach", "HEAD")
	if got := primaryTangleBranch(repo); got != "" {
		t.Errorf("primaryTangleBranch(detached HEAD) = %q, want \"\"", got)
	}
	git(t, repo, "checkout", "-q", "main")
}

func TestPrimaryTangleBranchNonGitDir(t *testing.T) {
	nd, err := os.MkdirTemp("", "parlay-guard-nogit-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(nd)
	if got := primaryTangleBranch(nd); got != "" {
		t.Errorf("primaryTangleBranch(nonGitDir) = %q, want \"\"", got)
	}
}

// ── guardRepo banner ─────────────────────────────────────────────────────

func TestGuardRepoEmitsBorderedBanner(t *testing.T) {
	repo := setupGuardRepo(t)
	git(t, repo, "checkout", "-q", "-b", "fm/readme-restructure-d3")
	var branch string
	out := captureStderr(t, func() {
		branch = guardRepo(repo, false)
	})
	git(t, repo, "checkout", "-q", "main")

	if branch != "fm/readme-restructure-d3" {
		t.Errorf("guardRepo returned %q, want %q", branch, "fm/readme-restructure-d3")
	}
	if !strings.Contains(out, "WORKTREE TANGLE") {
		t.Errorf("banner missing WORKTREE TANGLE, got: %s", out)
	}
	if !strings.Contains(out, "fm/readme-restructure-d3") {
		t.Errorf("banner missing branch name, got: %s", out)
	}
	want := "git -C " + repo + " checkout main"
	if !strings.Contains(out, want) {
		t.Errorf("banner missing non-destructive restore command %q, got: %s", want, out)
	}
}

func TestGuardRepoCleanPrimarySilent(t *testing.T) {
	repo := setupGuardRepo(t)
	git(t, repo, "checkout", "-q", "main")
	var branch string
	out := captureStderr(t, func() {
		branch = guardRepo(repo, false)
	})
	if branch != "" {
		t.Errorf("guardRepo(clean primary) = %q, want \"\"", branch)
	}
	if out != "" {
		t.Errorf("guardRepo(clean primary) wrote to stderr: %s", out)
	}
}

func TestGuardRepoReadOnlyDefersRestore(t *testing.T) {
	repo := setupGuardRepo(t)
	git(t, repo, "checkout", "-q", "-b", "feat/x")
	out := captureStderr(t, func() {
		guardRepo(repo, true)
	})
	git(t, repo, "checkout", "-q", "main")

	if !strings.Contains(out, "read-only session must leave restore") {
		t.Errorf("banner missing read-only guidance, got: %s", out)
	}
	if strings.Contains(out, "git -C") {
		t.Errorf("read-only banner should not include a restore command, got: %s", out)
	}
}

// mainWorktreePath sanity: not covered by commands-guard.test.ts directly,
// but exercised implicitly via variant.go — a minimal same-repo check here
// guards against a regression in the porcelain-parsing regex.
func TestMainWorktreePathResolvesPrimary(t *testing.T) {
	repo := setupGuardRepo(t)
	// git resolves the repo path (e.g. macOS /var -> /private/var symlink)
	// before printing it in `worktree list --porcelain`, so compare resolved
	// paths rather than the raw MkdirTemp string.
	wantResolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", repo, err)
	}
	got := mainWorktreePath(repo)
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", got, err)
	}
	if gotResolved != wantResolved {
		t.Errorf("mainWorktreePath(repo) resolved = %q, want %q", gotResolved, wantResolved)
	}
}
