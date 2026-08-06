---
"@parlay/cli": minor
---

Make `parlay merge-gate` verify head FRESHNESS, not just the check conclusion — a verdict is only about the commit that actually reached origin (robots-bn5d).

**READY was answering about a commit the caller had already replaced.** Every assertion the gate makes is evaluated against origin's `headRefOid`, which is correct — that is the commit a merge lands. But the caller is a mechanic who has just authored a fix and pushed it, and reads READY as "my fix is cleared to merge". On `trillium/firstmate#91` the fix had gone to the `no-mistakes` MIRROR and the pipeline had not yet pushed it to origin, so origin's head was still the pre-fix commit: the gate said READY, and merging there would have landed the old head and silently DROPPED the just-authored fix for the reviewer's finding. Minutes later, once the pipeline pushed, the same unchanged PR went `0` → `4`. The READY was not merely early — it was the opposite of the eventual truthful answer.

**What changed.** The gate now compares the LOCAL copy of the head branch against origin's `headRefOid` (`detectHeadFreshness`), pinned to the repository it already resolved (robots-g4qz) so a same-named branch in an unrelated checkout can never invent a blocker. It is read-only — never fetches, checks out, or writes — and works identically from a mechanic's isolated worktree, since refs are shared across linked worktrees.

- Origin's head sha is printed on EVERY verdict, so the caller can always see which commit the answer is about — including on READY (`Merging lands THAT commit — confirm it is the one you mean.`).
- A local branch AHEAD of (or DIVERGED from) origin's head is a `head-not-pushed` blocker: **pending**-class, exit `5`, on an open PR — nothing is wrong with the diff, the commits simply have not arrived, and the answer changes on its own once they do. On a MERGED PR it is **code**-class instead: the stale merge already happened, `git branch -r --contains <sha>` will pass for the wrong commit, and the work has to be pushed and re-landed. Head freshness runs BEFORE the MERGED short-circuit for exactly this reason.
- Exit `5` now has two shapes with opposite instructions, so its notes name whichever is present — "your push has not reached origin" vs. "the review is still running" — instead of always printing the review-in-flight script.
- Where no local branch is available to compare, the freshness is reported as UNVERIFIED with the one-line self-check the gate could not run, rather than implying the local and origin heads agree.

`AGENTS.md`, `claim.go`'s robots DoD, and `parlay merge-gate --help` all spell out the new exit-5 head-not-pushed contract: do not merge and do not edit, get the commits to origin, then re-run — and expect a fresh head to restart the review, so a `0` can legitimately become `3`/`4`/`5` afterwards.
