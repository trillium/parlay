// Teardown refusal telemetry (liveness lift unit 4): the stale marker a
// refused tree carries, and the edge-triggered skip tracker the sweep loop
// uses so a stable HOLD surfaces once per reason instead of once per tick.
// Both are ports of Gas City reaper machinery — writeWorktreeStaleMarker
// (session_worktree_prune.go) and reapSkipTracker (bead_worktree_reaper.go),
// the latter sized from a real incident where unchanged repeats were ~95% of
// telemetry (~260 MB/day).
package commands

import (
	"fmt"
	"os"
	"path/filepath"
)

// worktreeStaleMarkerName is the file a REFUSED pass leaves in the tree it
// could not remove. hasUncommitted deliberately ignores this one untracked
// file — it is teardown's own bookkeeping, not the agent's work.
const worktreeStaleMarkerName = ".worktree-stale"

// writeWorktreeStaleMarker records why worktree was refused, in the marker
// the next pass and `parlay guard` can read instead of re-deriving the
// reason. Best-effort, exactly as in Gas City: a write failure warns and
// never alters the caller's control flow — the refusal it annotates already
// protects the tree.
func writeWorktreeStaleMarker(worktree, reason string) {
	branch := ""
	if r := sh("git", "-C", worktree, "rev-parse", "--abbrev-ref", "HEAD"); r.ok {
		branch = r.out
	}
	content := fmt.Sprintf("branch=%s\nreason=%s\n", branch, reason)
	if err := os.WriteFile(filepath.Join(worktree, worktreeStaleMarkerName), []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "warn: writing %s marker for %s: %v\n", worktreeStaleMarkerName, worktree, err)
	}
}

// sweepSkipTracker suppresses unchanged repeats of per-agent HOLD / REFUSED /
// skip lines across `parlay sweep --interval` passes: a line is surfaced when
// the agent was not suppressed last pass, or is suppressed for a different
// reason now. Reporting only — the pass summary counters still count every
// agent on every pass, so the full picture never disappears.
//
// Owned by the single sweep loop and touched only between passes, so it
// carries no lock. A nil *sweepSkipTracker surfaces everything, preserving
// the unsuppressed behavior for one-shot sweeps that have no pass history to
// compare against.
type sweepSkipTracker struct {
	lastReason map[string]string   // agent id -> reason last surfaced
	thisPass   map[string]struct{} // ids evaluated in the pass under way
}

// newSweepSkipTracker returns a tracker with no recorded history, so the
// first pass through it surfaces every line.
func newSweepSkipTracker() *sweepSkipTracker {
	return &sweepSkipTracker{
		lastReason: make(map[string]string),
		thisPass:   make(map[string]struct{}),
	}
}

// beginPass starts a pass, clearing the set of ids seen so endPass can forget
// the ones that dropped out.
func (t *sweepSkipTracker) beginPass() {
	if t == nil {
		return
	}
	t.thisPass = make(map[string]struct{}, len(t.lastReason))
}

// shouldSurface records that id is being held back for reason during the pass
// under way, and reports whether that is news: true when the id was not held
// as of the previous pass, or was held for a different reason. False means an
// unchanged repeat the caller does not print.
func (t *sweepSkipTracker) shouldSurface(id, reason string) bool {
	if t == nil {
		return true
	}
	t.thisPass[id] = struct{}{}
	if prev, tracked := t.lastReason[id]; tracked && prev == reason {
		return false
	}
	t.lastReason[id] = reason
	return true
}

// endPass forgets every id the pass did not evaluate, bounding the tracker to
// the agents currently in the sweep and letting one that returns later
// surface again.
func (t *sweepSkipTracker) endPass() {
	if t == nil {
		return
	}
	for id := range t.lastReason {
		if _, seen := t.thisPass[id]; !seen {
			delete(t.lastReason, id)
		}
	}
}
