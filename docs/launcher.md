# Launcher (spawn)

**Code:** `bin/parlay-spawn` (bash, the original implementation) and `tools/parlay-bin` (`spawn.go` + `spawnpipeline.go` + `launcher.go`, a Go port).

`parlay spawn <agent-id> <display-name> <hex-color> <initial-prompt> ...`
launches a brand-new, top-level Claude Code session inside a detached
[herdr](https://github.com/trillium/herdr) terminal, registers it in the
[agent registry](agent-registry.md), posts a hello so its tab goes live
immediately, and hands it a startup prompt that arms `parlay listen`/`monitor`
on boot — so a spawned agent is a reachable chat tab from the moment it
starts, not after its own first enrollment call. It explicitly unsets
`CLAUDECODE`/`CLAUDE_CODE_*` before exec'ing, because herdr terminals inherit
the parent environment and Claude Code refuses to nest under those vars.

**`bin/parlay-spawn` is gated, not a free-standing script.** It refuses to run
unless `PARLAY_SPAWN_VIA_CLI=1` is already set — `parlay spawn` (the Go CLI)
is the sole sanctioned public entry point and sets that flag itself before
exec'ing the script (task-qyu8q scope 3). A model is a required argument on
every spawn, by the same ticket: nothing here silently inherits the launching
session's default model or falls back to sonnet without saying so.

**Two separate implementations exist and resolve differently depending on
`PATH`.** `tools/parlay-bin` is a newer Go port of the same launch flow, with
a richer surface (`--ephemeral` identities, `<id>=<repo>` batch dispatch,
`--worktree` isolation, `--account`). Per the root `CLAUDE.md`, when both are
on `PATH` the spawner-resolution order **prefers `tools/parlay-bin` over
`bin/parlay-spawn`** — and the Go port has **neither** the
`PARLAY_SPAWN_VIA_CLI` handshake gate **nor** the mandatory-model gate that
`bin/parlay-spawn` enforces (task-21d36). That is a genuine parity gap between
the two launchers, not a documentation gap — verified by reading both
scripts' argument handling on 2026-09-03; do not assume the Go port inherits
the bash script's safety gates just because it replaces it on `PATH`.

Post-launch, an optional watchdog (`PARLAY_SPAWN_NO_WATCHDOG=1` to disable)
confirms the spawned agent's first turn actually fired within
`PARLAY_SPAWN_LIVENESS_TIMEOUT_MS` (default 60s), using the same liveness
primitive `monitor.go`'s `watchdog.go` exposes — see [`docs/monitor.md`](monitor.md).

**Env vars** are documented in [`examples/env.example`](../examples/env.example); this doc does not re-list them.
