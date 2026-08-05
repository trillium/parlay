// `parlay teardown` — safe destroy of an agent that has a worktree.
//
// Ported from packages/cli/src/commands-teardown.ts (ticket B4). Refuses to
// destroy uncommitted changes or unpushed commits unless explicitly forced
// (--force). Validates that work is either committed + pushed, or that
// commits appear in a landed PR (falls back to a merge-tree equality test).
//
// Steps: 1) check uncommitted changes, 2) check unpushed commits, 3)
// validate landed-content containment, 4) deregister from the relay
// (best-effort), 5) remove the worktree, 6) delete the agent store.
package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// hasUncommitted reports whether repoPath has uncommitted changes.
func hasUncommitted(repoPath string) bool {
	r := sh("git", "-C", repoPath, "status", "--porcelain")
	return r.ok && r.out != ""
}

// hasUnpushed reports whether repoPath has commits not on any remote.
func hasUnpushed(repoPath string) bool {
	r := sh("git", "-C", repoPath, "log", "HEAD", "--not", "--remotes")
	return r.ok && r.out != ""
}

// isContentLanded validates landed-content containment via a merge-tree
// equality test: merge headRef into the default branch; if the tree is
// unchanged, content is isolated (already landed there).
func isContentLanded(repoPath, headRef string) bool {
	defBranch := sh("git", "-C", repoPath, "symbolic-ref", "refs/remotes/origin/HEAD")
	if !defBranch.ok {
		return false
	}
	branch := strings.Replace(defBranch.out, "refs/remotes/origin/", "", 1)
	mergeTree := sh("git", "-C", repoPath, "merge-tree", branch, headRef)
	if !mergeTree.ok {
		return false
	}
	return mergeTree.out == "" || strings.Contains(mergeTree.out, branch)
}

// bestEffortUnregister POSTs /api/chat/unregister and swallows every error
// (network failure, non-2xx status, whatever) — matching
// commands-teardown.ts's raw `fetch(...).catch(() => {})`, which neither
// checks the response status nor lets a failure propagate.
func bestEffortUnregister(agentID string) {
	payload, err := json.Marshal(map[string]string{"id": agentID})
	if err != nil {
		return
	}
	resp, err := httpc.Client.Post(config.ServerURL()+"/api/chat/unregister", "application/json", bytes.NewReader(payload))
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// Teardown is `parlay teardown`'s entry point.
func Teardown(argv []string) {
	if helpWanted("teardown", argv) {
		return
	}
	r := args.Parse("teardown", argv, []string{"--force"}, nil)
	agentID := ""
	if len(r.Positionals) > 0 {
		agentID = strings.TrimSpace(r.Positionals[0])
	}
	if agentID == "" {
		httpc.Die("parlay teardown: agent id required", config.ExitUsage)
		return
	}

	msg, err := teardownAgent(agentID, r.Bool("--force"))
	if err != nil {
		// Every refusal below is a usage error in the TS original's terms —
		// it exits 2 with the same message on stderr.
		httpc.Die(err.Error(), config.ExitUsage)
		return
	}
	fmt.Println(msg)
}

// teardownAgent is the safe-destroy chain itself, factored out of Teardown so
// `parlay sweep` can drive the SAME path per agent instead of duplicating the
// git safety checks (robots-6xq7). It returns the success line the caller
// prints, or the refusal as an error — it never calls os.Exit, so a sweep can
// refuse one agent and keep going. Warnings still go straight to stderr,
// matching the TS original's byte-for-byte output when driven by `teardown`.
func teardownAgent(agentID string, force bool) (string, error) {
	idHome := filepath.Join(parlayAgentsDir(), agentID)
	if _, err := os.Stat(idHome); err != nil {
		return "", fmt.Errorf("parlay teardown: agent '%s' not found in %s", agentID, idHome)
	}

	fm := readLocalFrontmatter(filepath.Join(idHome, "identity.md"))
	worktree := fm.Get("worktree")
	project := fm.Get("project")

	// No worktree — this agent isn't stranding work. Deregister + cleanup.
	if worktree == "" {
		bestEffortUnregister(agentID)
		os.RemoveAll(idHome)
		return fmt.Sprintf("agent %s torn down (no worktree)", agentID), nil
	}

	// Worktree already gone (stale reference).
	if _, err := os.Stat(worktree); err != nil {
		bestEffortUnregister(agentID)
		os.RemoveAll(idHome)
		return fmt.Sprintf("agent %s torn down (worktree already gone)", agentID), nil
	}

	// Check for uncommitted changes.
	if hasUncommitted(worktree) {
		if !force {
			// The refusals below are user-facing CLI messages printed verbatim to
			// stderr, not Go errors meant for wrapping — the trailing period and
			// second sentence are part of the TS original's byte-for-byte output,
			// so they must not be reworded to satisfy ST1005.
			//lint:ignore ST1005 verbatim CLI message; see comment above
			return "", fmt.Errorf("parlay teardown: %s has uncommitted changes. Run 'git diff' or --force to discard.", agentID)
		}
		fmt.Fprintf(os.Stderr, "warn: --force: discarding uncommitted changes in %s\n", worktree)
	}

	// Check for unpushed commits.
	if hasUnpushed(worktree) {
		if !force {
			head := sh("git", "-C", worktree, "rev-parse", "HEAD")
			if !head.ok || !isContentLanded(worktree, head.out) {
				//lint:ignore ST1005 verbatim CLI message, same as the uncommitted-changes refusal above
				return "", fmt.Errorf("parlay teardown: %s has unpushed commits not yet landed. Push or --force.", agentID)
			}
			fmt.Fprintf(os.Stderr, "teardown %s: unpushed commits but content is landed.\n", agentID)
		} else {
			fmt.Fprintf(os.Stderr, "warn: --force: discarding unpushed commits in %s\n", worktree)
		}
	}

	// Remove the worktree.
	if project != "" {
		rr := sh("git", "-C", project, "worktree", "remove", "--force", worktree)
		if !rr.ok {
			fmt.Fprintf(os.Stderr, "warn: worktree remove failed — %s\n", rr.err)
		}
	}

	// Deregister from relay (best-effort).
	bestEffortUnregister(agentID)

	// Delete agent store. Always delete — parlay agents are ephemeral by
	// default (firstmate keeps permanent agents' stores separately).
	os.RemoveAll(idHome)

	return fmt.Sprintf("agent %s torn down", agentID), nil
}
