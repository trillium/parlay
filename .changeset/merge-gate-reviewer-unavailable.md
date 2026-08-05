---
"@parlay/cli": minor
---

Give `parlay merge-gate` a bounded answer for "the reviewer is unavailable" — exit `4` NEEDS-DECISION, distinct from `3` BLOCKED (robots-8kkq).

**Non-zero was not a specific enough answer.** The gate already refused to trust a green CodeRabbit check that never ran, and that part works: on a rate-limited PR it correctly reports `vacuous-pass` + `review-rate-limited` and exits non-zero. But "a test is failing" and "CodeRabbit is rate limited" both came back as exit `3`, and they call for opposite behavior. The first is fixed by working on the PR. The second cannot be fixed from the PR at all — and in practice it outlasts its own stated window: `@coderabbitai review` recovered one PR once, then the account stayed limited across three further attempts over ~40 minutes, and `trillium/no-mistakes#7`'s follow-up commit merged with no bot review. A mechanic told only "blocked" has no terminating condition and polls until the night is gone.

**What changed.** Every `MergeBlocker` now carries a `Class`: `code` (fix it on the branch) or `reviewer-unavailable` (nothing on the branch changes it). When *every* blocker is reviewer-unavailability, the verdict is `NeedsDecision`, the header reads `NEEDS-DECISION`, the exit code is `4`, and the notes name the only two honest options — merge-and-disclose (land it and say plainly in the merge note that no review ran) or park (leave it open until the reviewer returns) — for the captain to choose. `4` is still non-zero, so a caller that only branches on the sign is unchanged and still fails closed.

The downgrade is deliberately narrow, because a needs-decision verdict is a request for the captain's attention and must not be spendable on ordinary work:

- One `code`-class blocker among them keeps the whole verdict at `3`. A failing test is still a failing test, whatever else is also wrong.
- `no-review-evidence` stays `code`-class. The gate cannot tell *why* nothing reviewed the PR, and unexplained gets the harsher code.
- `stale-review` is `code`-class on its own — push again and the reviewer catches up — and reclassifies to `reviewer-unavailable` only when a live rate-limit template sits alongside it, since that is the case where the re-review is precisely what is being refused. That pairing is the no-mistakes#7 shape.

`claim.go`'s robots DoD and `parlay merge-gate --help` now both spell out the exit-4 contract: do not poll, do not merge anyway, signal `parlay status needs-decision` with the gate's reason and stop.
