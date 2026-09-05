package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// gitToplevel resolves the git repo root containing cwd, or an error if cwd
// is not inside a git repo (or does not exist). Mirrors bash's
// `cd "$CWD" 2>/dev/null && git rev-parse --show-toplevel`.
func gitToplevel(cwd string) (string, error) {
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// realpath resolves symlinks the same way bash's `cd X && pwd -P` does, for
// the worktree isolation sanity check below.
func realpath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

// repoIdentity returns the absolute, symlink-resolved path of dir's COMMON git
// dir. Every linked worktree of a repo shares it with the primary checkout, and
// two different repos never share it — so it is the right key for "is this
// worktree actually a worktree of that repo?" (robots-d04t). Mirrors bash's
// repo_identity() in bin/parlay-spawn.
func repoIdentity(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", err
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return "", fmt.Errorf("%s: empty git common dir", dir)
	}
	if resolved, rErr := realpath(common); rErr == nil {
		return resolved, nil
	}
	return common, nil
}

// guardScriptPath resolves bin/parlay-treehouse-guard: PATH first (so an
// installed copy or a test stub wins), then the repo-relative location. This
// source file lives at tools/cli/internal/spawn/worktree.go, four directories
// below the repo root. Mirrors internal/monitor.scriptPath's precedence.
func guardScriptPath() (string, error) {
	if abs, err := exec.LookPath("parlay-treehouse-guard"); err == nil && abs != "" {
		return abs, nil
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate parlay-treehouse-guard: not on PATH and own source path unavailable")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	path := filepath.Join(root, "bin", "parlay-treehouse-guard")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("parlay-treehouse-guard not found on PATH or at %s", path)
	}
	return path, nil
}

// guardTreehousePool protects still-occupied pool slots before `treehouse get`
// is allowed to reclaim one (robots-n8d9). `treehouse get` checks origin/main
// out over whatever branch the slot held, and its eligibility rules could not
// see a live agent sitting in a clean slot — one run reclaimed a worktree out
// from under a running agent that belonged to a different firstmate home. The
// guard takes a protective treehouse lease on every occupied slot, which
// treehouse does honor. Strictly best effort: a missing or failing guard must
// never block a spawn, so every error here is a note on stderr, not a return.
func guardTreehousePool(projectPath string) {
	guard, err := guardScriptPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: treehouse pool guard unavailable (%v); continuing unguarded\n", err)
		return
	}
	cmd := exec.Command(guard, projectPath)
	cmd.Dir = projectPath
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: treehouse pool guard failed (%v); continuing unguarded\n", err)
	}
}

// setupWorktree creates (or reuses) an isolated git worktree for agentID
// under projectPath/.worktrees, per bin/parlay-spawn's worktree block
// (lines 364–409, fold §3.3). Tries treehouse first (firstmate-only tool,
// gracefully absent on minis), falls back to plain `git worktree add`.
// branch/pr mode creates a named branch `parlay/<id>`; report mode detaches.
// Hard isolation check at the end: the resolved worktree must not equal the
// resolved project root.
// The bool return reports whether treehouse actually leased the worktree
// (bash's local $TREEHOUSE_LEASED_PATH, set only in that branch — lines
// 1271, 1693): the subprocess launcher passes --worktree-path only when this
// is true, never for a plain git-worktree fallback.
func setupWorktree(projectPath, agentID, mode string) (string, bool, error) {
	worktreePath := filepath.Join(projectPath, ".worktrees", "parlay-"+agentID)
	if err := os.MkdirAll(filepath.Join(projectPath, ".worktrees"), 0o755); err != nil {
		return "", false, fmt.Errorf("mkdir .worktrees: %w", err)
	}

	// robots-d04t: treehouse has no --repo flag — it picks its pool from the
	// PROCESS cwd. Left inherited from the caller's shell, spawning with
	// --cwd ~/code/herdr-web from inside a firstmate worktree leased a
	// *firstmate* worktree and launched the agent in the wrong repo, silently.
	// Pin cmd.Dir to projectPath and verify the repo identity of whatever
	// treehouse hands back before trusting it.
	projRepo, projRepoErr := repoIdentity(projectPath)

	created := false
	if thPath, err := exec.LookPath("treehouse"); err == nil {
		guardTreehousePool(projectPath)
		cmd := exec.Command(thPath, "get", "--lease", "parlay-"+agentID)
		cmd.Dir = projectPath
		out, thErr := cmd.Output()
		if thErr == nil {
			// tr -d '[:space:]': strip ALL whitespace, not just trim ends.
			candidate := strings.Join(strings.Fields(string(out)), "")
			if fi, statErr := os.Stat(candidate); statErr == nil && fi.IsDir() {
				candRepo, candErr := repoIdentity(candidate)
				if candErr == nil && projRepoErr == nil && candRepo == projRepo {
					worktreePath = candidate
					created = true
					fmt.Fprintf(os.Stderr, "parlay-spawn: worktree via treehouse at %s\n", worktreePath)
				} else {
					fmt.Fprintf(os.Stderr, "parlay-spawn: WRONG-REPO WORKTREE — treehouse handed back %s (git dir: %s),\n", candidate, candRepo)
					fmt.Fprintf(os.Stderr, "  but --cwd resolved to %s (git dir: %s). Rejecting it and falling back to plain git worktree.\n", projectPath, projRepo)
					ret := exec.Command(thPath, "return", candidate)
					ret.Dir = projectPath
					_ = ret.Run()
				}
			}
		}
	}

	if !created {
		listOut, _ := exec.Command("git", "-C", projectPath, "worktree", "list", "--porcelain").Output()
		if strings.Contains(string(listOut), "worktree "+worktreePath+"\n") || strings.HasSuffix(strings.TrimRight(string(listOut), "\n"), "worktree "+worktreePath) {
			fmt.Fprintf(os.Stderr, "parlay-spawn: reusing existing worktree at %s\n", worktreePath)
		} else {
			var addErr error
			if mode == "branch" || mode == "pr" {
				addErr = exec.Command("git", "-C", projectPath, "worktree", "add", worktreePath, "-b", "parlay/"+agentID).Run()
				if addErr != nil {
					addErr = exec.Command("git", "-C", projectPath, "worktree", "add", worktreePath, "parlay/"+agentID).Run()
				}
			} else {
				addErr = exec.Command("git", "-C", projectPath, "worktree", "add", "--detach", worktreePath).Run()
			}
			if addErr != nil {
				return "", false, fmt.Errorf("git worktree add failed for %s: %w", worktreePath, addErr)
			}
			fmt.Fprintf(os.Stderr, "parlay-spawn: worktree created at %s (project: %s)\n", worktreePath, projectPath)
		}
	}

	wtReal, err1 := realpath(worktreePath)
	projReal, err2 := realpath(projectPath)
	if err1 == nil && err2 == nil && wtReal == projReal {
		return "", false, fmt.Errorf("ISOLATION FAILURE — worktree resolved to primary checkout (%s); aborting", projectPath)
	}

	// IDENTITY (robots-d04t): hard post-condition — the worktree we are about to
	// launch the agent in must be a worktree of the SAME repo --cwd resolved to.
	// Getting this wrong is silent and expensive (a full agent run spent in an
	// unrelated repo, possibly leaving commits/branches there), so refuse loudly.
	wtRepo, wtRepoErr := repoIdentity(worktreePath)
	if wtRepoErr != nil || projRepoErr != nil || wtRepo != projRepo {
		return "", false, fmt.Errorf("REPO MISMATCH — refusing to launch in the wrong repository.\n"+
			"  --cwd resolved to project: %s (git dir: %s)\n"+
			"  worktree resolved to:      %s (git dir: %s)\n"+
			"  These are different repos. Aborting instead of burning an agent run in the wrong one.",
			projectPath, projRepo, worktreePath, wtRepo)
	}

	return worktreePath, created, nil
}
