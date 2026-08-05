package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo makes a real git repo with one commit at dir and returns it.
func initRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// stubTreehouse puts a fake `treehouse` first on PATH for the duration of the
// test. The stub prints whatever `script` prints; `script` is a bash body with
// $1.. as the treehouse args and the process cwd as parlay-bin set it.
func stubTreehouse(t *testing.T, script string) {
	t.Helper()
	binDir := t.TempDir()
	stub := filepath.Join(binDir, "treehouse")
	body := "#!/usr/bin/env bash\n" + script + "\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write treehouse stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// robots-d04t: treehouse resolves its pool from the PROCESS cwd, so
// setupWorktree must run it with cwd = projectPath, not the caller's shell cwd.
// The stub echoes a path derived from its own cwd; if setupWorktree leaked the
// caller's cwd, it would name the WRONG repo's worktree.
func TestSetupWorktreeRunsTreehouseInProjectDir(t *testing.T) {
	root := t.TempDir()
	target := initRepo(t, filepath.Join(root, "target"))
	other := initRepo(t, filepath.Join(root, "other"))

	// Pre-make a real linked worktree of each repo, keyed by repo name, and have
	// the stub hand back the one matching ITS OWN cwd's repo.
	for name, repo := range map[string]string{"target": target, "other": other} {
		wt := filepath.Join(root, "pool", name)
		cmd := exec.Command("git", "-C", repo, "worktree", "add", "-q", "--detach", wt)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("worktree add %s: %v\n%s", name, err, out)
		}
	}
	stubTreehouse(t, `echo "`+root+`/pool/$(basename "$PWD")"`)

	// Caller's cwd is the OTHER repo — exactly the robots-d04t repro shape.
	origWD, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origWD) })
	if err := os.Chdir(other); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got, err := setupWorktree(target, "d04t", "report")
	if err != nil {
		t.Fatalf("setupWorktree: %v", err)
	}
	if filepath.Base(got) != "target" {
		t.Errorf("treehouse was run against the caller's cwd, not the project: got %s, want the 'target' pool worktree", got)
	}
	wantRepo, _ := repoIdentity(target)
	gotRepo, err := repoIdentity(got)
	if err != nil || gotRepo != wantRepo {
		t.Errorf("resolved worktree belongs to the wrong repo: got %s, want %s", gotRepo, wantRepo)
	}
}

// robots-d04t: even if treehouse hands back a worktree of some unrelated repo,
// setupWorktree must reject it rather than silently launch the agent there.
func TestSetupWorktreeRejectsWrongRepoTreehousePath(t *testing.T) {
	root := t.TempDir()
	target := initRepo(t, filepath.Join(root, "target"))
	other := initRepo(t, filepath.Join(root, "other"))

	wrong := filepath.Join(root, "pool", "wrong")
	cmd := exec.Command("git", "-C", other, "worktree", "add", "-q", "--detach", wrong)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	// Stub ignores cwd entirely and always returns the wrong repo's worktree.
	stubTreehouse(t, `echo "`+wrong+`"`)

	got, err := setupWorktree(target, "d04t", "report")
	if err != nil {
		t.Fatalf("setupWorktree: %v", err)
	}
	if got == wrong {
		t.Fatalf("accepted a worktree of an unrelated repo: %s", got)
	}
	// It must have fallen back to a real worktree of the target repo.
	wantRepo, _ := repoIdentity(target)
	gotRepo, err := repoIdentity(got)
	if err != nil || gotRepo != wantRepo {
		t.Errorf("fallback worktree belongs to the wrong repo: got %s, want %s", gotRepo, wantRepo)
	}
}

// repoIdentity must agree across a repo and every linked worktree of it, and
// must differ between two unrelated repos — the property the guard relies on.
func TestRepoIdentityDistinguishesRepos(t *testing.T) {
	root := t.TempDir()
	a := initRepo(t, filepath.Join(root, "a"))
	b := initRepo(t, filepath.Join(root, "b"))

	aWT := filepath.Join(root, "a-wt")
	if out, err := exec.Command("git", "-C", a, "worktree", "add", "-q", "--detach", aWT).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}

	aID, err := repoIdentity(a)
	if err != nil {
		t.Fatalf("repoIdentity(a): %v", err)
	}
	aWTID, err := repoIdentity(aWT)
	if err != nil {
		t.Fatalf("repoIdentity(a worktree): %v", err)
	}
	if aID != aWTID {
		t.Errorf("a repo and its own worktree disagree: %s vs %s", aID, aWTID)
	}
	bID, err := repoIdentity(b)
	if err != nil {
		t.Fatalf("repoIdentity(b): %v", err)
	}
	if aID == bID {
		t.Errorf("two unrelated repos share an identity: %s", aID)
	}
	if _, err := repoIdentity(filepath.Join(root, "not-a-repo")); err == nil {
		t.Error("repoIdentity should fail outside a git repo")
	}
}

// The bash source of truth must carry the same two fixes: treehouse pinned to
// $PROJECT_PATH, and a repo-identity post-condition before launch.
func TestBashSpawnPinsTreehouseToProjectPath(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "bin", "parlay-spawn"))
	if err != nil {
		t.Skipf("bin/parlay-spawn not readable: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, `$(cd "$PROJECT_PATH" && treehouse get --lease`) {
		t.Error("bin/parlay-spawn must run 'treehouse get' with cwd = $PROJECT_PATH (robots-d04t)")
	}
	if !strings.Contains(s, "REPO MISMATCH") || !strings.Contains(s, "repo_identity") {
		t.Error("bin/parlay-spawn must hard-check worktree repo identity against --cwd before launching (robots-d04t)")
	}
	if !strings.Contains(s, "security find-generic-password -a ccjuggler -s ccjuggler-NAME") {
		t.Error("bin/parlay-spawn --help must document the keychain lookup the way resolve_account_token actually queries it")
	}
}
