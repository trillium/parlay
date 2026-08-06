---
"@parlay/cli": minor
---

Stop `parlay merge-gate` reporting "blocked on the CODE" when the review simply has not finished — exit `5` PENDING, distinct from `3` BLOCKED (robots-rwf8).

**A running check is not a rejection.** The mechanic contract documents exit `3` as "blocked on the CODE: fix it on the branch". Observed on `trillium/no-mistakes#11`, a PR whose only blockers were

```
BLOCKED (2)
  ✗ check-pending        check "CodeRabbit" has not finished (Review in progress).
  ✗ no-review-evidence   nothing reviewed this PR...
```

exited `3`. Nothing was wrong with the diff — CodeRabbit had not started talking yet. Minutes later the same unchanged PR produced a genuine finding, and later still exited `0`. An agent obeying the documented contract goes editing a branch that has no defect, and the new push restarts the very review it was waiting on. It also breaks watchers: a monitor that treats `3` as terminal bails on its first poll. The only reason the observed session did not go and edit was that the blocker's free text happened to say "Review in progress" — the exit code alone was actively misleading.

**What changed.** `MergeBlocker.Class` gains a third value, `pending`, and the gate a third failure exit, `5`, with `Verdict.Pending` and a `PENDING` header to match. `check-pending` is `pending`-class. While a check is pending, so are `no-review-evidence` and `stale-review`: a running check *is* the explanation for both, which is exactly the thing the gate normally cannot infer and therefore treats harshly. `5` is still non-zero, so a caller that only branches on the sign is unchanged and still fails closed — the downgrade can never reach `0`.

Class precedence is **code > pending > reviewer-unavailable**:

- One `code`-class blocker keeps the whole verdict at `3`. A failing check or an unresolved review thread is still a finding about this code, whatever else is also running.
- Pending outranks needs-decision. Escalating to the captain while a review is mid-flight asks for a merge-and-disclose-or-park decision on information that is about to arrive; re-running the gate resolves it into a real `0`/`3`/`4`.
- A live rate-limit template still outranks an unfinished check — that reviewer has already answered, and the answer was no. `stale-review` under an active rate limit stays `reviewer-unavailable` (the no-mistakes#7 shape).
- A blocker whose class is unset counts as `code`. A forgotten class must never become a downgrade.

`claim.go`'s robots DoD and `parlay merge-gate --help` now spell out the exit-5 contract: do not edit the branch, do not merge, re-run the gate when the check reports — and bound the wait, because a check that never finishes is a `4`, not an excuse to poll forever (robots-8kkq).
