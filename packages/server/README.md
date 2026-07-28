# @parlay/server

This package is served as a module inside [Pulse](https://github.com/trillium) at
`~/.claude/PAI/PULSE/modules/chat` — that's the live, running copy. Historically
the two locations were separate file copies kept in sync by hand, which drifted
on every commit to either side.

## Structural parity

Every file in `src/` (except `package.json`) is now a **symlink** into
`~/.claude/PAI/PULSE/modules/chat/`. There is only one copy of the source on
disk; editing it from either path edits the same file. Parity can no longer
drift because there is nothing left to keep in sync.

`~/.claude/PAI/PULSE` is its own git repository (part of `~/.claude`), so this
package's git history stops reflecting line-level diffs of the linked files —
`git log` on this repo will show the symlink being created once, not the
ongoing edits. The real history of the server code lives in the `~/.claude`
repo from here on.

## Known tradeoff: `tools/split-test`

`tools/split-test` builds and runs a branch's *own* copy of the server to
compare behavior across branches. Because `src/*.ts` are now symlinks to a
single external, unbranched location, every split-test sandbox resolves to the
same PULSE-side code regardless of which parlay branch it's testing — branch-
specific server changes are no longer exercised in isolation by that tool.
This was a deliberate tradeoff to get real structural parity enforcement
without writing outside this repo/worktree; if per-branch split-testing of
server changes is needed again, `tools/split-test` would need to build from
`~/.claude/PAI/PULSE/modules/chat` directly (or that tool would need its own
copy step).
