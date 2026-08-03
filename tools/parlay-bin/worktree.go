package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// setupWorktree creates (or reuses) an isolated git worktree for agentID
// under projectPath/.worktrees, per bin/parlay-spawn's worktree block
// (lines 364–409, fold §3.3). Tries treehouse first (firstmate-only tool,
// gracefully absent on minis), falls back to plain `git worktree add`.
// branch/pr mode creates a named branch `parlay/<id>`; report mode detaches.
// Hard isolation check at the end: the resolved worktree must not equal the
// resolved project root.
func setupWorktree(projectPath, agentID, mode string) (string, error) {
	worktreePath := filepath.Join(projectPath, ".worktrees", "parlay-"+agentID)
	if err := os.MkdirAll(filepath.Join(projectPath, ".worktrees"), 0o755); err != nil {
		return "", fmt.Errorf("mkdir .worktrees: %w", err)
	}

	created := false
	if thPath, err := exec.LookPath("treehouse"); err == nil {
		out, thErr := exec.Command(thPath, "get", "--lease", "parlay-"+agentID).Output()
		if thErr == nil {
			// tr -d '[:space:]': strip ALL whitespace, not just trim ends.
			candidate := strings.Join(strings.Fields(string(out)), "")
			if fi, statErr := os.Stat(candidate); statErr == nil && fi.IsDir() {
				worktreePath = candidate
				created = true
				fmt.Fprintf(os.Stderr, "parlay-spawn: worktree via treehouse at %s\n", worktreePath)
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
				return "", fmt.Errorf("git worktree add failed for %s: %w", worktreePath, addErr)
			}
			fmt.Fprintf(os.Stderr, "parlay-spawn: worktree created at %s (project: %s)\n", worktreePath, projectPath)
		}
	}

	wtReal, err1 := realpath(worktreePath)
	projReal, err2 := realpath(projectPath)
	if err1 == nil && err2 == nil && wtReal == projReal {
		return "", fmt.Errorf("ISOLATION FAILURE — worktree resolved to primary checkout (%s); aborting", projectPath)
	}

	return worktreePath, nil
}
