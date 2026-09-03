# `Bun.spawn` env-snapshot and abort-non-rejection gotchas

Discovered building `examples/apple-notes-triage/` (discussion #244), where
`notes-io.ts`, `bead.ts`, and `spawn-triage.ts` all shell out via
`Bun.spawn`. Two non-obvious Bun runtime behaviors bit real production code
there, both verified with minimal standalone repros before being treated as
real.

## 1. `Bun.spawn` does not inherit live `process.env` mutations

`Bun.spawn([...], {...})` **without an explicit `env` key** does not see
runtime mutations to `process.env` made after Bun's own process started — it
behaves as though it snapshotted the environment at Bun startup, not at the
call site.

Repro: `process.env.FOO_TEST_VAR = "hello"` immediately before spawning
`bash -c 'echo $FOO_TEST_VAR'` prints an empty line without an explicit
`env`, and `hello` with `env: process.env` passed explicitly.

This matters anywhere test fixtures or callers set `process.env.X` at
runtime and expect a spawned child to see it (e.g. env-var-driven fixture
shims, `--account`-style config threaded through env). **Always pass
`env: process.env` explicitly** on any `Bun.spawn` call whose child needs to
see env set after process startup — don't rely on the default.

## 2. `AbortSignal.timeout` killing a spawned process does not reject its promises

When a `Bun.spawn(..., { signal: AbortSignal.timeout(ms) })` process is
killed by the timeout, `proc.exited` resolves **normally** with exit code
143 (128+SIGTERM) — it does not cause an awaited `Promise.all([...])`
including it to throw or reject. A `try { ... } catch` around the await will
never see the timeout as an error.

Fix pattern: manage your own `AbortController` + `setTimeout`, set a
`timedOut` boolean in the timeout callback *before* calling
`controller.abort()`, then check that flag explicitly after the awaited
promises settle — never infer a timeout from the exit code (a real process
could plausibly also exit 143) or from a catch block. See `runOsa` in
`examples/apple-notes-triage/notes-io.ts` for the working pattern.
