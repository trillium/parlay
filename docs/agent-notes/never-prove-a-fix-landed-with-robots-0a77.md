# Never prove a fix landed with a bare `gh pr view` — run `parlay landed <pr>` (robots-0a77)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`gh` resolving a bare PR number is the same defect as robots-g4qz above, in the
place it does the most damage: the mechanic contract's *proof of landing*. That
proof was written as two hand-run commands — `git branch -r --contains <sha>`
listing origin/main, plus `gh pr view <n> --json state,mergedAt` saying MERGED
— and gh prefers a remote named `upstream` over `origin`, so in a fork clone
the second command answers about the parent project's PR of the same number.
Live on robots-8bao: gh reported MERGED, mergedAt months earlier, for a
stranger's PR while trillium/no-mistakes#14 was still OPEN. Only the paired
git check caught it; the gh half alone is a false FIXED claim.

`parlay landed <pr> [--repo owner/name] [--branch <remote>/<branch>] [--json]`
(`tools/cli/internal/commands/landed.go`) folds both halves into one verb that
resolves the repo through the same `resolveMergeGateRepo` merge-gate uses, and
additionally requires a remote in *this* checkout to point at that repo — the
git and gh halves must describe the same repository or the proof means nothing.
Containment is proven for the **merge commit**, not the head sha (a squash or
rebase merge never puts the head on main). Exit codes: 0 landed, 3 the proof
ran and failed, 1 gh/git could not answer, 2 usage. Pure `ComputeLanded`,
tested with no gh and no network. Go-only, no TS port — same reasoning as
`merge-gate`; keep it out of `tools/cli/parity/run.sh`.

**Never parse `git branch` output — it is porcelain.** With `column.ui = always`
in the user's gitconfig, `git branch -r --contains <sha>` emits COLUMNS even
through a pipe: three refs on one line, so a line-per-ref parser matches nothing
and the verb reports NOT-LANDED for a fix that landed perfectly. `landed.go`
uses `git for-each-ref --contains <sha> --format=%(refname:short) refs/remotes`
— plumbing, one ref per line, no config to honor. Any new code here reading
branch lists must do the same.
