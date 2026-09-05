package spawn

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
// $1.. as the treehouse args and the process cwd as the spawn pipeline set it.
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

	got, _, err := setupWorktree(target, "d04t", "report")
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

	got, _, err := setupWorktree(target, "d04t", "report")
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

// robots-n8d9: `treehouse get` RESETS the slot it hands out, and its own
// eligibility rules cannot see a live agent sitting in a clean one — it
// checked origin/main out over a running agent's branch. The pool guard has to
// run BEFORE the lease request, pinned to the project dir like treehouse
// itself, or it protects nothing: once treehouse has answered, the displaced
// agent's checkout is already gone.
func TestSetupWorktreeGuardsPoolBeforeLeasing(t *testing.T) {
	root := t.TempDir()
	target := initRepo(t, filepath.Join(root, "target"))
	pool := filepath.Join(root, "pool", "slot")
	if out, err := exec.Command("git", "-C", target, "worktree", "add", "-q", "--detach", pool).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}

	binDir := t.TempDir()
	log := filepath.Join(root, "calls.log")
	stubs := map[string]string{
		"parlay-treehouse-guard": `printf 'guard %s\n' "$PWD" >> "` + log + `"`,
		"treehouse":              `printf 'treehouse %s %s\n' "$1" "$PWD" >> "` + log + `"; [ "$1" = get ] && echo "` + pool + `"; exit 0`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/usr/bin/env bash\n"+body+"\n"), 0o755); err != nil {
			t.Fatalf("write %s stub: %v", name, err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, _, err := setupWorktree(target, "n8d9", "report"); err != nil {
		t.Fatalf("setupWorktree: %v", err)
	}

	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("guard never ran and treehouse never ran: %v", err)
	}
	calls := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(calls) < 2 {
		t.Fatalf("expected a guard call and a treehouse call, got %q", calls)
	}
	if !strings.HasPrefix(calls[0], "guard ") {
		t.Errorf("pool guard did not run before `treehouse get`: call order %q", calls)
	}
	targetReal, _ := realpath(target)
	if gotDir := strings.TrimPrefix(calls[0], "guard "); gotDir != target && gotDir != targetReal {
		t.Errorf("guard ran against %s, not the project dir %s — it would sweep the wrong pool", gotDir, target)
	}
	if !strings.HasPrefix(calls[1], "treehouse get ") {
		t.Errorf("expected `treehouse get` right after the guard, got %q", calls[1])
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
