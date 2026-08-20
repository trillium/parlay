---
"@parlay/cli": minor
---

Say when merging a PR will not make the fix LIVE — `parlay merge-gate` reports origin lagging the deployed branch, and the robots DoD stops equating "merged" with "fixed" (robots-oex0).

**The proof of "landed" assumed the wrong artifact.** The mechanic contract proves a fix landed by showing `git branch -r --contains <sha>` lists `origin/main` and `gh pr view` says `MERGED`. That is a proof of liveness only if `origin/main` is the thing that runs. In `pai-hooks` it is not: `~/.claude/hooks` is a symlink into the checkout, so the hooks that actually execute are whatever **local** `main` says, and origin is a lagging mirror. Measured on that repo, `origin/main` was **20 commits behind** local `main` and 1 ahead of it (the squash of PR #5) — genuinely diverged in both directions. Both halves of the contract failed at once:

- a commit merged to `origin/main` satisfied "FIXED" without ever going live, and
- a commit that *was* live (`cdaf08f`) was not on `origin/main` at all, so it could never satisfy it.

The same drift produces the symptom mechanics hit first: a branch cut from local `main` drags all 20 unpushed commits into the PR, so GitHub reports an oversized diff and `mergeable=CONFLICTING`. PR #7 needed a `git rebase --onto origin/main main <branch>` to show its real 5-file change, with nothing in any tool naming the cause.

**What changed.** `MergeGateSnapshot` gains `Live` (`LiveBranchState`: `Known`/`Branch`/`Ahead`/`Behind`), filled by `detectLiveBranchDrift` — a read-only `git rev-list --left-right --count origin/<base>...<base>` against the PR's `baseRefName`, which `prViewFields` now requests. Refs are shared across linked worktrees, so it reads the same answer from a mechanic's isolated worktree as from the primary checkout.

When the local base branch is **ahead** of origin's copy, the verdict sets `OriginLagsLive` and the header says so — `MERGED — BUT NOT LIVE (origin lags the deployed branch)`, or `READY TO MERGE — BUT MERGING WILL NOT MAKE IT LIVE`. The liveness check runs *before* the already-MERGED short-circuit, because an already-merged PR in a lagging repo is exactly the case a mechanic misreads as done. Notes name the drift, the reconcile step (`merge` when the two have diverged, `pull --ff-only` when origin is only behind), and — only when the PR actually reports `CONFLICTING` — the `rebase --onto` that recovers the real diff.

**It is a note, never a blocker, and it never touches the exit code.** Nothing about the drift says anything bad about the diff, and nothing on the branch can fix it, so blocking would refuse merges that are perfectly fine — and a gate that cries wolf on every run in a repo like `pai-hooks` gets ignored on the run that matters. What the drift invalidates is the *inference drawn afterwards*, so that is where it is answered. A repo in sync stays silent, and an unmeasurable checkout (no git, no local base branch, no `origin/<base>` ref) leaves `Known` false and says nothing — "could not tell" is not "they agree", and only one of those licenses merged-means-fixed.

`claim.go`'s robots DoD now carries the consequence: where the gate prints `ORIGIN LAGS LIVE`, LANDED means the **deployed** branch contains the commit, not merely `origin/main` — reconcile the two before reporting done, or say plainly that the fix is merged but NOT live.
