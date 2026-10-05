# Need a real parlay instance to test against? `examples/bootstrap-sandbox.sh`

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`examples/` is a public, sanitized two-agent configuration (`parlay-state/` →
`~/.parlay`, `data-dir/` → the server's `-state-dir`), and
`examples/bootstrap-sandbox.sh` instantiates it in a `mktemp` sandbox on a
kernel-picked free port, builds `tools/cli` and `packages/go-server`, starts that
server, and asserts the round trip. Reach for it instead of hand-rolling another
throwaway instance — and read it before writing one, because it encodes the
isolation recipe.

**It went stale against the deleted TypeScript server and nobody noticed.**
It ran `cd packages/server && bun src/index.ts` against a directory that no
longer exists, so it failed at the first server start — while being advertised
in the README, in AGENTS.md, and in this note as *the* way to get a real
instance. Nothing caught it: the script stands up a real server fixture, so it
is deliberately excluded from the hermetic-harness CI job, and no doc check
executes a shell script. It stayed broken through the whole Bun→Go cutover. If
you touch anything that an onboarding path *runs*, run it.

**Redirect `$HOME`, not just the two variables.** `PARLAY_STATE_HOME` and
`PARLAY_AGENT_HOME` cover `identity`, `scratchpad`, `say`, `status` and
`doctor`, but `launch`, `teardown`, `variant` and `guard` resolve
`~/.parlay/agents` from `$HOME` directly and ignore `PARLAY_AGENT_HOME` (see
the B4/B9 notes above). `PAI_DIR` too — the server writes its TTS cache and
substitutions there, outside `-state-dir`.

`sweep` is the sharpest case, because it straddles the split from the other
direction: it *enumerates* and *classifies* out of `$PARLAY_AGENT_HOME` but the
teardown it then *performs* resolves `~/.parlay/agents` from `$HOME` — so a
half-redirected `sweep --apply` judges the example's agents and then looks for
them under your real home. It fails toward held, but redirect `$HOME` rather than
relying on that. `examples/README.md` has the per-variable breakdown.

Two traps it exists to keep you out of: `pkill -f 'bun src/index.ts'` matches
**every** worktree's sandbox server on this box, not just yours (the script
kills its own recorded pid instead); and you should build and invoke the Go
binary directly rather than through `bin/parlay`, because in a clone of this
repo the `parlay` on `PATH` is a symlink to the repo's own wrapper and building
over it replaces that wrapper for everything else on the machine. (The wrapper no
longer exports a `PARLAY_SERVER` of its own — it deliberately exports nothing, so
`config.json` resolves the same either way.)

Anything added to the example ships publicly — it is derived from the captain's
live setup, so keep every value a stand-in and re-run the script before
committing.
