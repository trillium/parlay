// `parlay variant teardown` git-safety tests (robots-cncx). The variant
// teardown path removes its worktree with `git worktree remove --force`, so
// anything it does not refuse first is permanently destroyed. These pin the
// refusals — uncommitted changes, unpushed-and-unlanded commits — plus the
// --force override and the clean-worktree happy path.
//
// Every test redirects $HOME: parlayAgentsDir()/parlayWktreesDir() hardcode
// homedir()/.parlay/{agents,worktrees} and honor no override (see guard.go).
//
// Ordering note: httpc.Die panics under withExitTrap, so captureStderr must
// wrap withExitTrap and not the other way round — a panic escaping through
// captureStderr never assigns its return value.
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/worktreeliveness"
)

// variantFixture builds a real repo with a real origin, a variant worktree
// under $HOME/.parlay/worktrees/<id>, and the variant's agent store. Returns
// the variant id and its worktree path.
func variantFixture(t *testing.T) (variantID, wkPath string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	origin := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatalf("MkdirAll origin: %v", err)
	}
	git(t, origin, "init", "-q", "--bare", "-b", "main")

	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.email", "t@t.t")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, repo, "remote", "add", "origin", origin)
	git(t, repo, "push", "-q", "-u", "origin", "main")

	variantID = "primary-wt1"
	wkPath = filepath.Join(home, ".parlay", "worktrees", variantID)
	if err := os.MkdirAll(filepath.Dir(wkPath), 0o755); err != nil {
		t.Fatalf("MkdirAll worktrees: %v", err)
	}
	git(t, repo, "worktree", "add", "-q", wkPath, "-b", "parlay-variant/"+variantID)
	git(t, wkPath, "config", "user.email", "t@t.t")
	git(t, wkPath, "config", "user.name", "t")

	// These tests pin the GIT refusals, so the pre-git gates (liveness,
	// freshness — teardown_gates.go) are satisfied here: a stubbed idle scan,
	// and a worktree aged past the quarantine.
	stubLiveness(t, worktreeliveness.StateOf())
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(wkPath, ".git"), old, old); err != nil {
		t.Fatalf("Chtimes .git: %v", err)
	}

	store := filepath.Join(home, ".parlay", "agents", variantID)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatalf("MkdirAll store: %v", err)
	}
	body := "---\nid: " + variantID + "\nvariant_of: primary\n---\n# Identity\n\n"
	if err := os.WriteFile(filepath.Join(store, "identity.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile identity.md: %v", err)
	}
	return variantID, wkPath
}

// newUnregisterServer answers POST /api/chat/unregister — variantTeardown's
// unregister call is NOT best-effort (it dies on failure, see variant.go), so
// the success-path tests need a live endpoint.
func newUnregisterServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/unregister", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestVariantTeardownRefusesUncommittedChanges(t *testing.T) {
	variantID, wkPath := variantFixture(t)
	if err := os.WriteFile(filepath.Join(wkPath, "wip.txt"), []byte("unsaved work\n"), 0o644); err != nil {
		t.Fatalf("WriteFile wip: %v", err)
	}

	var code int
	var exited bool
	stderr := captureStderr(t, func() {
		code, exited = withExitTrap(t, func() { variantTeardown([]string{variantID}) })
	})

	if !exited || code != 2 {
		t.Fatalf("variant teardown with uncommitted changes: exited=%v code=%d, want exit 2", exited, code)
	}
	if !strings.Contains(stderr, "has uncommitted changes") {
		t.Errorf("stderr = %q, want it to name the uncommitted changes", stderr)
	}
	if _, err := os.Stat(filepath.Join(wkPath, "wip.txt")); err != nil {
		t.Errorf("refused teardown destroyed the worktree anyway: %v", err)
	}
}

func TestVariantTeardownRefusesUnpushedCommits(t *testing.T) {
	variantID, wkPath := variantFixture(t)
	if err := os.WriteFile(filepath.Join(wkPath, "feature.txt"), []byte("landed nowhere\n"), 0o644); err != nil {
		t.Fatalf("WriteFile feature: %v", err)
	}
	git(t, wkPath, "add", "-A")
	git(t, wkPath, "commit", "-q", "-m", "unpushed work")

	var code int
	var exited bool
	stderr := captureStderr(t, func() {
		code, exited = withExitTrap(t, func() { variantTeardown([]string{variantID}) })
	})

	if !exited || code != 2 {
		t.Fatalf("variant teardown with unpushed commits: exited=%v code=%d, want exit 2", exited, code)
	}
	if !strings.Contains(stderr, "unpushed commits not yet landed") {
		t.Errorf("stderr = %q, want it to name the unpushed commits", stderr)
	}
	if _, err := os.Stat(filepath.Join(wkPath, "feature.txt")); err != nil {
		t.Errorf("refused teardown destroyed the worktree anyway: %v", err)
	}
}

func TestVariantTeardownForceDiscardsUncommittedChanges(t *testing.T) {
	variantID, wkPath := variantFixture(t)
	t.Setenv("PARLAY_SERVER", newUnregisterServer(t).URL)
	if err := os.WriteFile(filepath.Join(wkPath, "wip.txt"), []byte("expendable\n"), 0o644); err != nil {
		t.Fatalf("WriteFile wip: %v", err)
	}

	var code int
	var exited bool
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			code, exited = withExitTrap(t, func() { variantTeardown([]string{variantID, "--force"}) })
		})
	})

	if exited {
		t.Fatalf("variant teardown --force exited with code %d, want completion", code)
	}
	if !strings.Contains(stdout, "torn down") {
		t.Errorf("stdout = %q, want the torn-down confirmation", stdout)
	}
	if !strings.Contains(stderr, "discarding uncommitted changes") {
		t.Errorf("stderr = %q, want the --force discard warning", stderr)
	}
	if _, err := os.Stat(wkPath); !os.IsNotExist(err) {
		t.Errorf("worktree %s still present after --force teardown", wkPath)
	}
}

func TestVariantTeardownProceedsOnCleanWorktree(t *testing.T) {
	variantID, wkPath := variantFixture(t)
	t.Setenv("PARLAY_SERVER", newUnregisterServer(t).URL)

	var code int
	var exited bool
	stdout := captureStdout(t, func() {
		captureStderr(t, func() {
			code, exited = withExitTrap(t, func() { variantTeardown([]string{variantID}) })
		})
	})

	if exited {
		t.Fatalf("variant teardown on a clean worktree exited with code %d, want completion", code)
	}
	if !strings.Contains(stdout, "torn down") {
		t.Errorf("stdout = %q, want the torn-down confirmation", stdout)
	}
	if _, err := os.Stat(wkPath); !os.IsNotExist(err) {
		t.Errorf("worktree %s still present after teardown", wkPath)
	}
}

// The git check must run before mergeKind writes into the primary's store — a
// refused teardown must leave the primary untouched, so a later retry still
// has everything to merge.
func TestVariantTeardownRefusalDoesNotMergeIntoPrimary(t *testing.T) {
	variantID, wkPath := variantFixture(t)
	home := os.Getenv("HOME")
	primaryStore := filepath.Join(home, ".parlay", "agents", "primary")
	if err := os.MkdirAll(primaryStore, 0o755); err != nil {
		t.Fatalf("MkdirAll primary: %v", err)
	}
	variantScratch := filepath.Join(home, ".parlay", "agents", variantID, "scratchpad.md")
	if err := os.WriteFile(variantScratch, []byte("# Scratchpad\n\n- [2026-08-05] a novel note\n"), 0o644); err != nil {
		t.Fatalf("WriteFile scratchpad: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wkPath, "wip.txt"), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatalf("WriteFile wip: %v", err)
	}

	captureStderr(t, func() {
		withExitTrap(t, func() { variantTeardown([]string{variantID}) })
	})

	if _, err := os.Stat(filepath.Join(primaryStore, "scratchpad.md")); !os.IsNotExist(err) {
		t.Errorf("refused teardown merged into the primary's scratchpad anyway (err=%v)", err)
	}
}
