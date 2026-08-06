---
"@parlay/cli": patch
---

`parlay merge-gate`: a rate-limited *check description* is a refusal too, and exit `4` now names the way out (robots-eowy).

**The gate pointed at the code for a reviewer-availability problem.** On `trillium/no-mistakes#13` it printed `vacuous-pass` + `stale-review` and exited `3` — the code the mechanic contract documents as "blocked on the CODE: fix it on the branch" — while the truth was that CodeRabbit had reviewed the first push and then refused to re-review the new head. Exit `3` is the worst possible answer there: it sends a mechanic hunting a defect that does not exist, and every edit it makes pushes a new head, restarting the review and re-consuming the very limit that is blocking it.

The reclassification introduced for robots-8kkq only read the rate-limit *comment*, and in this shape there isn't one. CodeRabbit edits its single comment in place, so the walkthrough from the earlier review sits on the PR forever and the refusal appears only in the check description — the same free-text field the gate already reads to catch a vacuous pass. A refusal is a refusal wherever it is written down, so a vacuous check now reclassifies `stale-review` and `no-review-evidence` to `reviewer-unavailable` exactly as a rate-limit comment does.

Extending it to `no-review-evidence` is not a widening of the downgrade. That blocker was kept `code`-class for one stated reason — the gate cannot tell *why* nothing reviewed the PR, and unexplained gets the harsher code — and a check that says it did not run is precisely that missing knowledge. A **green** check still explains nothing, so an unexplained missing review keeps exit `3`, and one `code`-class blocker among them (a failing check, an unresolved thread) still keeps the whole verdict at `3`.

**Exit 4 also stopped being a dead end.** It said stop, but not that stopping is permanent, so the natural reading — "wait, then re-run the gate" — deadlocks: CodeRabbit does not re-review when the window lapses; it reviews only on a new push or an explicit `@coderabbitai review` comment, so nothing will ever ask it again. The verdict now says so, tells the caller not to edit the branch and why, and offers the captain three options — re-request, merge-and-disclose, park — instead of two. `parlay merge-gate --help` and `claim.go`'s robots DoD carry the same contract.

The gate deliberately does **not** post that comment itself. It is a read-only verb whose whole value is being truthful about what happened, and a gate invoked in a poll loop would spam the reviewer and re-consume the limit at issue. Naming the action is the fix; taking it is the captain's call.
