---
"parlay-cli": patch
---

fix(merge-gate): resolve the repo from `origin`, not gh's upstream-first guess (robots-g4qz)

`parlay merge-gate <pr>` let `gh` pick the repository implicitly. gh's
base-repo resolution prefers a remote named `upstream` over `origin`, so in a
fork clone — origin=`trillium/<repo>` plus an `upstream` remote, which is every
clone the fleet works in — the gate read the *upstream* project's PR of the same
number. PR numbers collide freely between the two, and the failure was silent: a
perfectly well-formed verdict about somebody else's pull request. Worst case it
was exit 0, "PR is already MERGED — nothing left to gate", for a fork PR that
was still open and unreviewed — a merge gate failing OPEN, the exact thing the
verb exists to prevent.

The repo is now resolved once, up front: explicit `--repo` wins, else the
`origin` remote (where the fleet's PRs live, and the same remote the mechanic
contract's `git branch -r --contains` proof checks against), and only a checkout
with no usable origin falls back to gh's own pick. That one answer is passed to
every `gh` call in the run, so `pr view`, `pr checks` and the review-thread
GraphQL query can no longer disagree about which repository they describe.

Every verdict now prints the repo it answered about and how it was chosen —
above even the already-MERGED short-circuit — and warns when GitHub answers for
a different repository than the one requested.
