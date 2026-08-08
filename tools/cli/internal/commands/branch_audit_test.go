// Tests for branch-audit (robots-d988). The regressions that matter here are
// about DIFF DIRECTION, which a hand-built snapshot cannot express — the whole
// defect is that two-dot `git diff origin/main <branch>` answers a different
// question than the reader asked. So the shape tests build real throwaway
// repositories with `git` and assert on what branch-audit reads back out of
// them. ComputeBranchAudit's own policy (behind is never a blocker, the
// branch's own deletions are never a blocker) is pinned separately with pure
// snapshots.
package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// gitRepo is a throwaway repository with a fluent-enough helper set to build
// the exact histories robots-90i7 and robots-l0ev produced.
type gitRepo struct {
	t   *testing.T
	dir string
}

func newGitRepo(t *testing.T) *gitRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	r := &gitRepo{t: t, dir: t.TempDir()}
	r.git("init", "--quiet", "--initial-branch=main")
	r.git("config", "user.email", "audit@example.test")
	r.git("config", "user.name", "Branch Audit Test")
	r.git("config", "commit.gpgsign", "false")
	return r
}

func (r *gitRepo) git(args ...string) string {
	r.t.Helper()
	c := exec.Command("git", append([]string{"-C", r.dir}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE=2026-08-06T12:00:00Z",
		"GIT_COMMITTER_DATE=2026-08-06T12:00:00Z",
	)
	out, err := c.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes files (path -> content) and commits them. A nil content means
// delete the path.
func (r *gitRepo) commit(msg string, files map[string]string) string {
	r.t.Helper()
	for path, content := range files {
		full := filepath.Join(r.dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	r.git("add", "-A")
	r.git("commit", "--quiet", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func (r *gitRepo) rm(msg string, paths ...string) string {
	r.t.Helper()
	r.git(append([]string{"rm", "--quiet"}, paths...)...)
	r.git("commit", "--quiet", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func (r *gitRepo) audit(t *testing.T, branch, base string) (BranchAuditSnapshot, BranchAuditVerdict) {
	t.Helper()
	snap, err := readBranchAudit(r.dir, branch, base)
	if err != nil {
		t.Fatalf("readBranchAudit: %v", err)
	}
	return snap, ComputeBranchAudit(snap)
}

// TestStaleBranchIsNotAReversion is the robots-90i7 reproduction, and the
// single most important case in this file. A branch is cut, main then LANDS
// four new files, and the branch adds work of its own. `git diff main <branch>`
// reports those four files as deleted; branch-audit must report zero deletions
// and a clean verdict, with the behind-ness as its own note.
func TestStaleBranchIsNotAReversion(t *testing.T) {
	r := newGitRepo(t)
	r.commit("base", map[string]string{"README.md": "hello\n"})

	// Every file gets distinct content so git's rename detection cannot pair a
	// main-only file with a branch-only one and hide it from --diff-filter=D;
	// the precondition below is asserting the raw two-dot artifact, so it has
	// to be the artifact and nothing else.
	r.git("checkout", "--quiet", "-b", "feature")
	r.commit("add provenance work", map[string]string{
		"provenance/check.sh": "#!/bin/sh\n# provenance check\nexit 0\n",
		"provenance/lib.sh":   "# provenance lib\n",
	})

	// main advances with work the branch has never seen — PR #92 / PR #101 in
	// the original report.
	r.git("checkout", "--quiet", "main")
	r.commit("PR #92: pool reclaim", map[string]string{
		"bin/fm-pool-reclaim.sh":        "#!/bin/sh\n# pool reclaim\n",
		"tests/fm-pool-reclaim.test.sh": "#!/bin/sh\n# pool reclaim tests\n",
	})
	r.commit("PR #101: agent axi", map[string]string{
		"bin/fm-agent-axi.sh":              "#!/bin/sh\n# agent axi\n",
		"tests/fm-test-parlay-guard.te.sh": "#!/bin/sh\n# parlay guard tests\n",
	})

	// The artifact this verb exists to replace: tip-vs-tip says four deletions.
	twoDot := r.git("diff", "--diff-filter=D", "--name-only", "main", "feature")
	if len(strings.Split(twoDot, "\n")) != 4 {
		t.Fatalf("precondition: expected the two-dot diff to invent 4 deletions, got %q", twoDot)
	}

	snap, v := r.audit(t, "feature", "main")

	if len(snap.DeletedFiles) != 0 {
		t.Errorf("true deletions = %v, want none — the branch deleted nothing", snap.DeletedFiles)
	}
	if snap.Deletions != 0 {
		t.Errorf("true deleted lines = %d, want 0", snap.Deletions)
	}
	if snap.FilesChanged != 2 || snap.Insertions == 0 {
		t.Errorf("true contribution = %d files / %d insertions, want the branch's own 2 added files",
			snap.FilesChanged, snap.Insertions)
	}
	if len(snap.AddedFiles) != 2 {
		t.Errorf("added files = %v, want the branch's own 2", snap.AddedFiles)
	}
	if snap.Behind != 2 {
		t.Errorf("behind = %d, want 2", snap.Behind)
	}
	if snap.Ahead != 1 {
		t.Errorf("ahead = %d, want 1", snap.Ahead)
	}
	if v.Stripped {
		t.Errorf("verdict says stripped; a stale branch reverts nothing: %+v", v.Strips)
	}
	if v.ExitCode != config.ExitOK {
		t.Errorf("exit = %d, want 0 — being behind must never be non-zero", v.ExitCode)
	}
	if !containsSubstr(v.Notes, "behind") || !containsSubstr(v.Notes, "staleness, not reversion") {
		t.Errorf("notes must name staleness explicitly, got %v", v.Notes)
	}

	report := FormatBranchAudit(snap, v)
	if !strings.HasPrefix(report, "CLEAN") {
		t.Errorf("report should lead CLEAN, got:\n%s", report)
	}
	for _, invented := range []string{"fm-pool-reclaim", "fm-agent-axi"} {
		if strings.Contains(report, invented) {
			t.Errorf("report names %s, a file the branch never touched:\n%s", invented, report)
		}
	}
}

// TestMergeDroppingAParentsAdditionIsAStrip is the union-merge shape the
// original report was right to worry about: a merge commit that silently loses
// a file one of its parents had ADDED. Nothing on the branch authored that
// delete, so it is a real strip and must be non-zero.
func TestMergeDroppingAParentsAdditionIsAStrip(t *testing.T) {
	r := newGitRepo(t)
	r.commit("base", map[string]string{"README.md": "hello\n"})

	// main adds a file.
	r.commit("main adds a tool", map[string]string{"bin/tool.sh": "#!/bin/sh\necho tool\n"})
	mainTip := r.git("rev-parse", "HEAD")

	// A branch off the base does its own work, then merges main in — but the
	// merge resolution drops main's new file.
	r.git("checkout", "--quiet", "-b", "feature", "HEAD~1")
	r.commit("feature work", map[string]string{"feature.txt": "work\n"})
	r.git("merge", "--quiet", "--no-commit", "--no-ff", mainTip)
	r.git("rm", "--quiet", "--cached", "bin/tool.sh")
	if err := os.Remove(filepath.Join(r.dir, "bin/tool.sh")); err != nil {
		t.Fatal(err)
	}
	r.git("commit", "--quiet", "-m", "Merge main into feature (superset)")

	snap, v := r.audit(t, "feature", "main")

	if !v.Stripped {
		t.Fatalf("a merge that dropped a parent's added file must be STRIPPED; notes=%v", v.Notes)
	}
	if v.ExitCode != ExitBranchStripped {
		t.Errorf("exit = %d, want %d", v.ExitCode, ExitBranchStripped)
	}
	if len(v.Strips) != 1 || v.Strips[0].File != "bin/tool.sh" {
		t.Fatalf("strips = %+v, want exactly bin/tool.sh", v.Strips)
	}
	if len(snap.Merges) != 1 {
		t.Errorf("merges audited = %d, want 1", len(snap.Merges))
	}
	if report := FormatBranchAudit(snap, v); !strings.HasPrefix(report, "STRIPPED") {
		t.Errorf("report should lead STRIPPED, got:\n%s", report)
	}
}

// TestMergeHonoringADeliberateDeleteIsNotAStrip is the other side of the same
// classifier. A file that existed before the split and was deleted on one side
// is SUPPOSED to be absent from the merge; calling that a strip would be a new
// false positive, which is the exact failure mode this verb exists to remove.
func TestMergeHonoringADeliberateDeleteIsNotAStrip(t *testing.T) {
	r := newGitRepo(t)
	r.commit("base", map[string]string{
		"README.md":   "hello\n",
		"bin/old.sh":  "#!/bin/sh\n",
		"keep/why.md": "keep\n",
	})

	// main deliberately retires bin/old.sh.
	r.rm("main retires old.sh", "bin/old.sh")
	mainTip := r.git("rev-parse", "HEAD")

	// The branch never saw that delete, and merging main in honors it.
	r.git("checkout", "--quiet", "-b", "feature", "HEAD~1")
	r.commit("feature work", map[string]string{"feature.txt": "work\n"})
	r.git("merge", "--quiet", "--no-ff", "-m", "Merge main into feature", mainTip)

	snap, v := r.audit(t, "feature", "main")

	if v.Stripped {
		t.Fatalf("honoring a deliberate delete is resolution, not a strip: %+v", v.Strips)
	}
	if v.ExitCode != config.ExitOK {
		t.Errorf("exit = %d, want 0", v.ExitCode)
	}
	if len(snap.Merges) != 1 || len(snap.Merges[0].Resolved) != 1 {
		t.Fatalf("want the delete recorded as resolved, got merges=%+v", snap.Merges)
	}
	if snap.Merges[0].Resolved[0].File != "bin/old.sh" {
		t.Errorf("resolved file = %q, want bin/old.sh", snap.Merges[0].Resolved[0].File)
	}
	if !containsSubstr(v.Notes, "not a strip") {
		t.Errorf("notes should explain the resolution, got %v", v.Notes)
	}
}

// TestBranchDeletingItsOwnFileIsNotAStrip pins that ordinary work stays exit 0.
// Deleting a file is a normal thing for a branch to do; a verb that blocked on
// it would be unusable and would teach the fleet to ignore it.
func TestBranchDeletingItsOwnFileIsNotAStrip(t *testing.T) {
	r := newGitRepo(t)
	r.commit("base", map[string]string{"README.md": "hello\n", "bin/dead.sh": "#!/bin/sh\n"})

	r.git("checkout", "--quiet", "-b", "feature")
	r.rm("drop the dead script on purpose", "bin/dead.sh")

	snap, v := r.audit(t, "feature", "main")

	if v.Stripped || v.ExitCode != config.ExitOK {
		t.Fatalf("the branch's own delete must stay exit 0, got exit=%d strips=%+v", v.ExitCode, v.Strips)
	}
	if len(snap.DeletedFiles) != 1 || snap.DeletedFiles[0] != "bin/dead.sh" {
		t.Errorf("deleted files = %v, want [bin/dead.sh]", snap.DeletedFiles)
	}
	if !containsSubstr(v.Notes, "ordinary work, not a reversion") {
		t.Errorf("notes should name the deletion as ordinary work, got %v", v.Notes)
	}
}

// TestBaseResolutionPrefersRemoteTracking pins that with no --base the verb
// reads origin/HEAD rather than the local default branch, because a pooled
// clone's local main is routinely stale — the same reason fm-review-diff.sh
// fetches before comparing.
func TestBaseResolutionPrefersRemoteTracking(t *testing.T) {
	r := newGitRepo(t)
	r.commit("base", map[string]string{"README.md": "hello\n"})
	// Fabricate a remote-tracking default without a real remote.
	r.git("update-ref", "refs/remotes/origin/main", "HEAD")
	r.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	base, err := resolveBranchAuditBase(r.dir)
	if err != nil {
		t.Fatalf("resolveBranchAuditBase: %v", err)
	}
	if base != "origin/main" {
		t.Errorf("base = %q, want origin/main", base)
	}
}

// TestUnrelatedHistoriesRefuseRatherThanGuess: with no merge-base there is no
// honest comparison, so the verb must say so instead of falling back to a
// tip-vs-tip diff — the fallback would reintroduce the whole defect.
func TestUnrelatedHistoriesRefuseRatherThanGuess(t *testing.T) {
	r := newGitRepo(t)
	r.commit("base", map[string]string{"README.md": "hello\n"})
	r.git("checkout", "--quiet", "--orphan", "orphan")
	r.git("rm", "-rf", "--quiet", ".")
	r.commit("unrelated root", map[string]string{"other.txt": "other\n"})

	_, err := readBranchAudit(r.dir, "orphan", "main")
	if err == nil {
		t.Fatal("want an error for unrelated histories, got none")
	}
	if !strings.Contains(err.Error(), "common ancestor") {
		t.Errorf("error should name the missing common ancestor, got %v", err)
	}
}

// TestComputeBranchAuditPolicy pins the decision layer with no repository:
// staleness and the branch's own deletions are facts, only a strip is a
// blocker.
func TestComputeBranchAuditPolicy(t *testing.T) {
	t.Run("badly behind with big deletions is still clean", func(t *testing.T) {
		v := ComputeBranchAudit(BranchAuditSnapshot{
			Branch: "feature", Base: "origin/main",
			Behind: 16, Ahead: 3,
			FilesChanged: 21, Insertions: 1214, Deletions: 0,
			DeletedFiles: []string{"bin/retired.sh"},
		})
		if v.Stripped || v.ExitCode != config.ExitOK {
			t.Fatalf("exit=%d stripped=%v, want a clean verdict", v.ExitCode, v.Stripped)
		}
		if !containsSubstr(v.Notes, "16 commits behind") {
			t.Errorf("notes should state the behind count, got %v", v.Notes)
		}
	})

	t.Run("one strip anywhere makes the whole verdict non-zero", func(t *testing.T) {
		v := ComputeBranchAudit(BranchAuditSnapshot{
			Branch: "feature", Base: "origin/main",
			Merges: []BranchMergeAudit{
				{SHA: "d8313f1e", Resolved: []BranchStrip{{File: "a"}}},
				{SHA: "aa8b69ae", Strips: []BranchStrip{{Merge: "aa8b69ae", File: "bin/tool.sh"}}},
			},
		})
		if !v.Stripped || v.ExitCode != ExitBranchStripped {
			t.Fatalf("exit=%d stripped=%v, want %d/true", v.ExitCode, v.Stripped, ExitBranchStripped)
		}
		if len(v.Strips) != 1 {
			t.Errorf("strips = %+v, want the one real strip", v.Strips)
		}
	})

	t.Run("up to date says so", func(t *testing.T) {
		v := ComputeBranchAudit(BranchAuditSnapshot{Branch: "feature", Base: "origin/main"})
		if !containsSubstr(v.Notes, "up to date") {
			t.Errorf("notes = %v, want an up-to-date line", v.Notes)
		}
	})
}

func TestParseNumstat(t *testing.T) {
	files, ins, del := parseNumstat("3\t1\ta.go\n10\t0\tb.go\n-\t-\timg.png\n")
	if files != 3 || ins != 13 || del != 1 {
		t.Errorf("got %d/%d/%d, want 3/13/1 (binary counts as a file only)", files, ins, del)
	}
	if f, i, d := parseNumstat(""); f != 0 || i != 0 || d != 0 {
		t.Errorf("empty numstat = %d/%d/%d, want zeros", f, i, d)
	}
}

func containsSubstr(notes []string, want string) bool {
	for _, n := range notes {
		if strings.Contains(n, want) {
			return true
		}
	}
	return false
}
