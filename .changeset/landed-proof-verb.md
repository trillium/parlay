---
"@parlay/cli": patch
---

feat(landed): fold the mechanic's landing proof into a verb that pins the repo (robots-0a77)

The mechanic contract's proof of landing named two commands: `git branch -r
--contains <sha>` must list origin/main, and `gh pr view <n> --json
state,mergedAt` must say MERGED. The second half was prescribed as a *bare* gh
command, and a bare gh command does not mean what it reads like — gh's
base-repo resolution prefers a remote named `upstream` over `origin`, so in a
fork clone (every clone the fleet works in) it answers about the parent
project's PR of the same number.

Observed live on robots-8bao: `gh pr view 14 --json state,mergedAt` returned
state=MERGED, mergedAt=2026-04-12 for an unrelated upstream PR from months
earlier, while trillium/no-mistakes#14 — the real PR — was still OPEN at a
different head. Only the paired `git branch -r --contains` check caught it;
the gh half alone was a false FIXED claim, the single outcome the guardrail
exists to prevent, produced by the command the guardrail told the mechanic to
run.

`parlay landed <pr> [--repo owner/name] [--branch <remote>/<branch>] [--json]`
is the replacement. It resolves the repository the way `merge-gate` already
does (robots-g4qz: explicit `--repo` > the `origin` remote > gh's own pick, and
only with no usable origin), passes it to gh explicitly, and asserts both
halves together: a remote in *this* checkout points at the repository being
asked about, GitHub answered about that same repository, the PR is MERGED, and
the commit it produced is reachable from the remote base branch. Neither half
alone is sufficient — the gh half alone is the fork defect above, and the git
half alone cannot tell a merged PR from a commit pushed straight to main.

Containment is proven for the *merge commit*, not the head sha, so a squash or
rebase merge is judged correctly; the remote is fetched once when the commit is
not present locally. Exit codes are fail-closed: 0 landed, 3 the proof ran and
failed, 1 gh/git could not answer, 2 usage. Decision logic is the pure
`ComputeLanded(LandedSnapshot)`, unit-tested with no gh binary and no network.

The containment check shells out to `git for-each-ref --contains`, not
`git branch -r --contains`: `git branch` is porcelain, and a user with
`column.ui = always` gets its output in COLUMNS even through a pipe, so a
line-per-ref parser matches nothing and reports NOT-LANDED for a fix that
landed. Caught on this change's own smoke test against a merged PR.

`claim.go`'s robots DoD now routes the landing proof through `parlay landed`
and says explicitly not to hand-run the bare `gh pr view`.
