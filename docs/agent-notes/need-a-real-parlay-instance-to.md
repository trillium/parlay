# Need a real parlay instance to test against? `examples/bootstrap-sandbox.sh`

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`examples/` is a public, sanitized two-agent configuration (`parlay-state/` →
`~/.parlay`, `data-dir/` → `$PARLAY_DATA_DIR`), and
`examples/bootstrap-sandbox.sh` instantiates it in a `mktemp` sandbox on a
kernel-picked free port, builds `tools/cli`, starts `packages/server`, and
asserts the round trip. Reach for it instead of hand-rolling another throwaway
instance — and read it before writing one, because it encodes the isolation
recipe: redirect **`$HOME`** as well as `PARLAY_DATA_DIR`/`PARLAY_STATE_HOME`/
`PARLAY_AGENT_HOME`, since `launch`/`teardown`/`variant`/`guard` resolve
`~/.parlay/agents` from `$HOME` and ignore `PARLAY_AGENT_HOME` (see the B4/B9
notes above). `PAI_DIR` too — see the `PARLAY_DATA_DIR` section above for why it
is not covered.

`sweep` is the sharpest case, because it straddles that split: a half-redirected
`sweep --apply` judges the REAL agent store against a redirected keep-list, and
it is the verb that deletes stores and removes worktrees. It fails toward held,
but redirect `$HOME` rather than relying on that. `examples/README.md` has the
per-variable breakdown.

Two traps it exists to keep you out of: `pkill -f 'bun src/index.ts'` matches
**every** worktree's sandbox server on this box, not just yours (the script
kills its own recorded pid instead); and `bin/parlay` exports
`PARLAY_SERVER=http://localhost:31337`, which outranks `config.json`, so a
sandbox must build and invoke the Go binary directly.

Anything added to the example ships publicly — it is derived from the captain's
live setup, so keep every value a stand-in and re-run the script before
committing.
