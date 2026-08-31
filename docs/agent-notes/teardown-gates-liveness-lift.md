# Teardown is gated fail-closed, and `--force` does not mean what it used to

**One-liner in CLAUDE.md:** teardown/sweep refuse on lease, liveness, borrow,
freshness, git state, and stashes; `--force` bypasses only the inspectable git
half; a refused tree carries a `.worktree-stale` marker.

## Where this came from

The Gas City liveness lift (epic task-4cfpv.10; PRs #138, #139, #142, #144)
ported the ordering discipline of Gas City's bead-worktree reaper
(`~/code/gascity/cmd/gc/bead_worktree_reaper.go`, read-only reference) onto
parlay's one teardown enforcement point. The authoritative design is the
scope-liveness-lift report in firstmate's data dir; the rulings that bind
future edits are restated here.

## The gate order (all in `checkWorktreeGitSafetyLive` → `checkWorktreePreGitSafety`)

1. **Treehouse lease** — a worktree with a `.treehouse` path component is a
   leased pool slot parlay does not own the allocation of. Refused, always.
   No release verb is confirmed to exist (tracked follow-up), so the gate
   refuses-and-reports rather than releasing.
2. **Liveness** — `internal/worktreeliveness` (lsof cwd scan, honest three-way
   outcome). An *indeterminate* scan (`Scanned=false`) refuses everything: an
   empty process listing means the probe failed, not that the host is idle.
3. **Borrow-veto** — any *other* agent whose `~/.parlay/agents/<id>/identity.md`
   points `worktree:` at the same path, and whose status is not terminal
   (`done`/`failed`), vetoes. A read error on any identity.md aborts the scan
   and protects every remaining candidate.
4. **Freshness quarantine** — a worktree whose `.git` pointer is younger than
   10 minutes (override: `$PARLAY_STATE_HOME/teardown-min-age-minutes`, `0`
   disables) is refused; un-stat-able age also refuses.
5. **Git state** — uncommitted changes, unpushed-and-unlanded commits
   (unchanged from B4/robots-ceon — the merge-tree `--write-tree` containment
   test must never be replaced with a reachability test), and **stashes**
   (`git stash list` non-empty or unreadable refuses; new in the lift).

## `--force` semantics (report ruling §6.3 — do not widen)

`--force` bypasses **freshness and the git-state gates only** — things an
operator can inspect before typing it. It NEVER bypasses lease, liveness, or
borrow-veto: those describe *someone else's* stake in the tree, which no flag
can assert away. If a genuine need appears, add an explicit second flag
(`--force-live`); do not widen `--force`.

## Removal, marker, and sweep-loop telemetry (unit 4)

- Removal is **non-force first**; on failure the full safety chain re-runs
  with *fresh* probes, and only a still-clean tree is retried with `--force`.
  A failed re-check returns the refusal and leaves the tree intact.
- Every refusal best-effort writes `.worktree-stale` (`branch=…`/`reason=…`)
  into the tree. `hasUncommitted` filters exactly that one untracked file;
  keep that filter or a transient refusal becomes a permanent one.
- `parlay sweep --interval` prints HOLD/REFUSED/skip lines edge-triggered
  (on reason change) via `sweepSkipTracker`; one-shot sweeps surface
  everything. Summary counters always count every agent.

## Plumbing rules that keep this correct

- Probes are batched per sweep pass in `teardownProbes` (lazy lsof + lazy
  borrow index); thread it caller-down, never re-probe per candidate
  (robots-8783), and never put I/O inside `ClassifySweep` (robots-6xq7).
- Tests stub the seams (`collectWorktreeLiveness`, `collectBorrowIndex`) —
  CI is a Linux box where a real scan's answer is meaningless. Real-directory
  coverage lives in the `scanBorrowIndex` tests under a redirected `$HOME`.
- Every gate added here only ADDS refusals. Keep it that way: the lift's
  contract is monotonically safety-increasing.
