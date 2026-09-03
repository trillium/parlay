# The bun CI job's `*.test.ts` coverage gate is repo-wide

`.github/workflows/ci.yml`'s `bun` job has an "uncovered" check before it runs
any tests: it globs `git ls-files '*.test.ts' '*.test.tsx'` across the
**entire repository** and fails the job if any match falls outside its
hardcoded `$roots` list (`packages/input packages/client packages/server
packages/webview packages/ccjuggler tools`).

That glob is not scoped to the bun workspace (`package.json`'s `workspaces`
is `packages/*` only). A test file added anywhere else in the tree — for
example under `examples/`, which is deliberately outside the workspace —
still trips the gate if it's named `*.test.ts` or `*.test.tsx`, because the
check runs before the per-root `bun test` loop and doesn't care whether the
file's directory is a workspace member.

Discovered while adding `examples/sms-voice-prototype/`
(task-7bnwy — SMS voice-flow prototype for discussion #240): a
`grammar.test.ts` there would have failed CI even though the directory has
no `package.json` and is never touched by any `bun test` invocation.

## What to do

Pick one, depending on whether the test should run in CI:

- **The code intentionally lives outside the workspace and should stay
  untested by root CI** (e.g. a standalone `examples/` prototype with its
  own `bun test` instructions in a README): name the test file `.spec.ts`
  (or any pattern other than `.test.ts`/`.test.tsx`) so the gate's glob
  never sees it. `bun test` still discovers `.spec.ts` files fine when run
  from inside that directory manually.
- **The code should be covered by CI**: add its directory to the `$roots`
  list in `.github/workflows/ci.yml`, same as any `packages/*` member.

Do not just add the file and hope — the gate is designed to turn a silently
skipped test file into a hard CI failure, and it will.
