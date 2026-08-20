---
"@parlay/cli": minor
---

Stop `parlay merge-gate` reporting "blocked on the CODE" when a check failed without ever running the code — exit `6` INFRA, distinct from `3` BLOCKED (robots-6mw2).

**Red that never touched the diff.** GitHub Actions jobs die during action setup:

```
##[error]Failed to resolve action download info. Error: Service Unavailable
##[error]Service Unavailable
```

No repo code executes, no test output is produced — and the check reports `bucket=fail` with an **empty description**, which is indistinguishable by status alone from a genuinely failing test. Three `trillium/firstmate` runs failed that way in one afternoon (31114515763, 31114626679, 31119180717) and every PR open at the time showed red that had nothing to do with its diff. The gate landed all of it in `3`, which the mechanic contract documents as "blocked on the CODE: fix it on the branch" — sending an agent hunting a defect in code that never ran, on a branch whose only problem is GitHub's availability. It is the exact sibling of the vacuous pass (robots-jap6): **a check that failed without running says as little about the diff as a check that passed without running.**

**What changed.** `MergeBlocker.Class` gains a fourth value, `infra`, and the gate a fourth failure exit, `6`, with `Verdict.Infra` and an `INFRA` header. A failing or cancelled check is classified by its check-run **annotations**, not its description — GitHub Actions leaves the description empty, but a job that ran the code always annotates `Process completed with exit code N`, while a job that died in setup annotates only GitHub's own error text. The new blocker code is `check-did-not-run`, and the notes name the recovery: `gh run rerun <run-id> --failed --repo owner/name` (with the real run id, parsed from the check's link), then re-run the gate.

**The downgrade is evidence-gated in both directions**, because a gate must fail closed:

- It requires **at least one** infra-shaped annotation **and no** annotation that looks like the code failing. A test that fails while asserting on a 503 prints "Service Unavailable" all over its log but still annotates the step's exit, so it stays `code`.
- Unreadable annotations, an empty annotation list on a failed job, a non-Actions check (CodeRabbit's link is empty — there is no annotations endpoint to ask), or any unrecognized failure text all keep the check `code`-class.
- A **cancelled** job with no verdict of its own is `infra` — cancellation is by definition an ending before a verdict, and in the observed runs it was the cascade half of the same incident. A cancelled job that *did* report a real failure first stays `code`.
- `The job has exceeded the maximum execution time of …` is deliberately **not** an infra signature. A hung test in the diff produces exactly that annotation, so it keeps pointing at the branch even though a starved runner can produce it too.

Class precedence is now **code > pending > infra > reviewer-unavailable**. Pending outranks infra because `gh run rerun` refuses a run with jobs still in flight — advising a re-run before the run finishes is advice that cannot be followed. Infra outranks reviewer-unavailable because re-running the jobs is a bounded step the mechanic can take alone, where needs-decision is terminal until the captain picks. One `code`-class blocker still keeps the whole verdict at `3`, and an unset class still counts as `code`, so no downgrade can launder a real failure.

Only failing checks pay for the extra annotations request; a green PR costs exactly the same three calls it always did. `claim.go`'s robots DoD and `parlay merge-gate --help` document the exit-6 contract, including its bound: if a re-run dies on the same infra signature, that is a GitHub incident, not your branch — signal `parlay status needs-decision` rather than re-running forever (robots-8kkq).
