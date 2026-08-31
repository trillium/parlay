// `parlay teardown` — safe destroy of an agent that has a worktree.
//
// Ported from packages/cli/src/commands-teardown.ts (ticket B4). Refuses to
// destroy uncommitted changes or unpushed commits unless explicitly forced
// (--force). Validates that work is either committed + pushed, or that its
// content is already present in the default branch (a merge-tree equality
// test — see isContentLanded). There is no PR/patch-id strategy here; the
// docs that claimed one were wrong (robots-ceon).
//
// Steps: 1) check uncommitted changes, 2) check unpushed commits, 3)
// validate landed-content containment, 4) deregister from the relay
// (best-effort), 5) remove the worktree, 6) delete the agent store, 7) close
// the agent's herdr pane/tab (best-effort — see herdr.go; robots-iz9o).
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

// hasUncommitted reports whether repoPath has uncommitted changes. The one
// exclusion: an untracked stale marker (written by a previous REFUSED pass —
// see writeWorktreeStaleMarker) is teardown's own bookkeeping, not the
// agent's work. Without this filter, any refusal on a clean tree (freshness,
// borrow) would convert itself into a permanent uncommitted-changes refusal
// on every later pass.
func hasUncommitted(repoPath string) bool {
	r := sh("git", "-C", repoPath, "status", "--porcelain")
	if !r.ok {
		return false
	}
	for _, line := range strings.Split(r.out, "\n") {
		if line == "" || line == "?? "+worktreeStaleMarkerName {
			continue
		}
		return true
	}
	return false
}

// hasUnpushed reports whether repoPath has commits not on any remote.
func hasUnpushed(repoPath string) bool {
	r := sh("git", "-C", repoPath, "log", "HEAD", "--not", "--remotes")
	return r.ok && r.out != ""
}

// isContentLanded validates landed-content containment via a merge-tree
// equality test: three-way merge headRef into the default branch and compare
// the resulting tree against the default branch's own tree. When headRef
// introduces nothing the default branch does not already have (e.g. its change
// landed as a squash commit, so the original commits are unreachable from any
// remote), the merged tree is byte-identical to the default branch's tree.
// Comparing trees — rather than commits — is what makes squash-merged work
// detectable at all.
//
// This replaces a version that shelled out to two-arg `git merge-tree <branch>
// <head>` and then tested `out == "" || strings.Contains(out, branch)`
// (robots-ceon). On git >= 2.38 that form prints a bare tree OID, so `out` was
// never empty, and a branch name like "main" can never appear in 40 hex digits
// — the function returned false unconditionally and the landed escape in
// teardownAgent had never once fired. The correct form, mirrored from
// firstmate's bin/fm-teardown.sh `content_in_default`, is `merge-tree
// --write-tree` (git >= 2.38) compared against the real default-branch tree.
//
// Every inconclusive path returns false so teardown refuses rather than
// guesses: no origin/HEAD, no resolvable default ref, a merge conflict, or a
// git too old for --write-tree (which exits non-zero on the unknown flag).
func isContentLanded(repoPath, headRef string) bool {
	defBranch := sh("git", "-C", repoPath, "symbolic-ref", "refs/remotes/origin/HEAD")
	if !defBranch.ok {
		return false
	}
	branch := strings.Replace(defBranch.out, "refs/remotes/origin/", "", 1)

	// Refresh the remote-tracking ref first: the whole point of this check is
	// work that landed upstream after the worktree last synced, which a stale
	// origin/<branch> cannot see. Best-effort — an offline teardown falls
	// through to whatever ref already exists rather than refusing outright.
	remoteRef := "refs/remotes/origin/" + branch
	if sh("git", "-C", repoPath, "remote", "get-url", "origin").ok {
		sh("git", "-C", repoPath, "fetch", "--quiet", "origin",
			"+refs/heads/"+branch+":"+remoteRef)
	}

	// Prefer the remote-tracking ref (what "landed" actually means); fall back
	// to the local branch for a repo with no origin at all.
	ref := remoteRef
	if !sh("git", "-C", repoPath, "rev-parse", "--quiet", "--verify", ref).ok {
		ref = "refs/heads/" + branch
		if !sh("git", "-C", repoPath, "rev-parse", "--quiet", "--verify", ref).ok {
			return false
		}
	}

	defaultTree := sh("git", "-C", repoPath, "rev-parse", "--quiet", "--verify", ref+"^{tree}")
	if !defaultTree.ok || defaultTree.out == "" {
		return false
	}

	// --write-tree prints the merged tree OID on the first line; on conflict it
	// exits non-zero and prints the conflict report instead.
	mergeTree := sh("git", "-C", repoPath, "merge-tree", "--write-tree", ref, headRef)
	if !mergeTree.ok {
		return false
	}
	merged := mergeTree.out
	if i := strings.IndexByte(merged, '\n'); i >= 0 {
		merged = merged[:i]
	}
	merged = strings.TrimSpace(merged)

	return merged != "" && merged == defaultTree.out
}

// checkWorktreeGitSafety is the git half of a safe destroy: it refuses to
// remove a worktree that still holds uncommitted changes, or commits that are
// neither pushed nor landed, unless force is set. Returns nil when the
// worktree is safe to remove (or force overrode the refusal).
//
// Factored out of teardownAgent so `parlay variant teardown` can share it:
// that path used to jump straight to `git worktree remove --force` with zero
// git checks, permanently destroying a variant's working tree while `parlay
// teardown` refused the identical situation (robots-cncx).
//
// cmd is the user-facing command prefix ("parlay teardown", "parlay variant
// teardown"). The rest of each refusal is byte-for-byte the TS original's:
// these are CLI messages printed verbatim to stderr, not Go errors meant for
// wrapping, so the trailing period and second sentence must not be reworded to
// satisfy ST1005.
func checkWorktreeGitSafety(cmd, agentID, worktree string, force bool) error {
	return checkWorktreeGitSafetyLive(cmd, agentID, worktree, force, nil)
}

// checkWorktreeGitSafetyLive is checkWorktreeGitSafety with the caller's
// pre-collected probe set threaded in (nil self-serves through the seams in
// teardown_gates.go). The pre-git gates — treehouse lease, process liveness,
// borrow-veto, freshness quarantine — run FIRST: they answer "does anyone
// else have a stake in this tree", which moots the git questions, and all but
// freshness are gates --force cannot bypass (see teardown_gates.go).
func checkWorktreeGitSafetyLive(cmd, agentID, worktree string, force bool, probes *teardownProbes) error {
	if err := checkWorktreePreGitSafety(cmd, agentID, worktree, force, probes); err != nil {
		return err
	}
	// Check for uncommitted changes.
	if hasUncommitted(worktree) {
		if !force {
			return fmt.Errorf("%s: %s has uncommitted changes. Run 'git diff' or --force to discard.", cmd, agentID) //nolint:staticcheck
		}
		fmt.Fprintf(os.Stderr, "warn: --force: discarding uncommitted changes in %s\n", worktree)
	}

	// Check for unpushed commits.
	if hasUnpushed(worktree) {
		if !force {
			head := sh("git", "-C", worktree, "rev-parse", "HEAD")
			if !head.ok || !isContentLanded(worktree, head.out) {
				return fmt.Errorf("%s: %s has unpushed commits not yet landed. Push or --force.", cmd, agentID) //nolint:staticcheck
			}
			fmt.Fprintf(os.Stderr, "teardown %s: unpushed commits but content is landed.\n", agentID)
		} else {
			fmt.Fprintf(os.Stderr, "warn: --force: discarding unpushed commits in %s\n", worktree)
		}
	}

	// Check for stashes (liveness lift unit 3; Gas City checks these in its
	// git-state gate, parlay never did anywhere). Unlike hasUncommitted /
	// hasUnpushed — whose probe failures fall open for parity with the TS
	// original — a failed stash probe refuses: `git worktree remove` destroys
	// stashes as surely as it destroys uncommitted files, and this gate is
	// new, so it owes no fidelity to a fail-open past. --force bypasses both
	// cases: stash state is operator-inspectable, same as the other git gates.
	if st := sh("git", "-C", worktree, "stash", "list"); !st.ok {
		if !force {
			return fmt.Errorf("%s: %s stash state unreadable (git stash list failed). Triage by hand or --force.", cmd, agentID) //nolint:staticcheck
		}
		fmt.Fprintf(os.Stderr, "warn: --force: stash state unreadable in %s; removing anyway\n", worktree)
	} else if st.out != "" {
		if !force {
			return fmt.Errorf("%s: %s has stashed changes. Apply or drop the stash, or --force to discard.", cmd, agentID) //nolint:staticcheck
		}
		fmt.Fprintf(os.Stderr, "warn: --force: discarding stashed changes in %s\n", worktree)
	}
	return nil
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
	return teardownAgentLive(agentID, force, nil)
}

// teardownAgentLive is teardownAgent with the caller's pre-collected probe
// set: a sweep pass pays for ONE process-table scan and ONE borrow-index walk
// and shares them across every candidate (the same batching robots-8783
// forced on the relay lookup, applied to heavier probes). nil self-serves per
// call.
func teardownAgentLive(agentID string, force bool, probes *teardownProbes) (string, error) {
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
		return fmt.Sprintf("agent %s torn down (no worktree)%s", agentID, closeHerdrSurface(agentID)), nil
	}

	// Worktree already gone (stale reference).
	if _, err := os.Stat(worktree); err != nil {
		bestEffortUnregister(agentID)
		os.RemoveAll(idHome)
		return fmt.Sprintf("agent %s torn down (worktree already gone)%s", agentID, closeHerdrSurface(agentID)), nil
	}

	// Refuse to destroy a leased, live, borrowed, quarantined, uncommitted,
	// or unlanded tree — and record the reason in the tree itself, so the
	// next pass and `parlay guard` read it instead of re-deriving it.
	if err := checkWorktreeGitSafetyLive("parlay teardown", agentID, worktree, force, probes); err != nil {
		writeWorktreeStaleMarker(worktree, err.Error())
		return "", err
	}

	// Remove the worktree — non-force first (liveness lift unit 4): git's own
	// refusal to remove a dirty or locked tree is a second line of defence
	// behind the gates above, exactly as in Gas City's reaper. On failure,
	// re-run the FULL safety check with fresh probes — the caller's scan is
	// pass-scoped and a process may have arrived since — and only when the
	// tree still proves safe retry with --force, which keeps the pre-unit-4
	// success rate for the mechanical failures (our own stale marker, ignored
	// files, a stale lock) that non-force removal trips on.
	if project != "" {
		rr := sh("git", "-C", project, "worktree", "remove", worktree)
		if !rr.ok {
			if err := checkWorktreeGitSafetyLive("parlay teardown", agentID, worktree, force, nil); err != nil {
				writeWorktreeStaleMarker(worktree, err.Error())
				return "", err
			}
			rr = sh("git", "-C", project, "worktree", "remove", "--force", worktree)
			if !rr.ok {
				fmt.Fprintf(os.Stderr, "warn: worktree remove failed — %s\n", rr.err)
			}
		}
	}

	// Deregister from relay (best-effort).
	bestEffortUnregister(agentID)

	// Delete agent store. Always delete — parlay agents are ephemeral by
	// default (firstmate keeps permanent agents' stores separately).
	os.RemoveAll(idHome)

	return fmt.Sprintf("agent %s torn down%s", agentID, closeHerdrSurface(agentID)), nil
}
