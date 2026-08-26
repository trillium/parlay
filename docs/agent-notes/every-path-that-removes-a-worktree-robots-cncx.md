# Every path that removes a worktree must go through `checkWorktreeGitSafety` (robots-cncx)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`parlay variant teardown` merged the variant's notes, then ran `git worktree
remove --force` with **no git check at all** — uncommitted changes in a
variant's worktree were permanently destroyed, while `parlay teardown` refused
the identical situation. Its `--force` flag only ever gated *unmerged memory*,
so the safe-looking bare invocation was the destructive one.

The uncommitted/unpushed/landed-content checks now live in one place —
`checkWorktreeGitSafety(cmd, agentID, worktree, force)` in
`tools/cli/internal/commands/teardown.go` (mirrored in
`packages/cli/src/commands-teardown.ts`) — called by `teardownAgent` and by
`variantTeardown`, which runs it **before** `mergeKind` writes into the primary
so a refusal leaves nothing half-merged. `--force` now means "discard the
working tree too", same as `parlay teardown`. Regression coverage:
`variant_test.go` (real repo + real origin + real worktree, `$HOME` redirected
because `parlayWktreesDir()` honors no override).

Rule for anything new here: `git worktree remove --force` is unconditional
destruction — route it through `checkWorktreeGitSafety` rather than adding a
fourth ad hoc check, and put the git gate ahead of any state mutation so a
refusal is a genuine no-op.
