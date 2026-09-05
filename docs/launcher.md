# Launcher (spawn)

**Code:** `tools/cli/internal/spawn` (`spawn.go` + `spawnpipeline.go` + `launcher.go`, the Go implementation `parlay spawn` runs in-process) and `bin/parlay-spawn` (bash, the original implementation, now an escape hatch).

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
unless `PARLAY_SPAWN_VIA_CLI=1` is already set — `parlay spawn` is the sole
sanctioned public entry point, and it sets that flag itself before exec'ing the
script under `PARLAY_SPAWN_IMPL=bash` (task-qyu8q scope 3). The default
in-process Go path never sets it and has no such check: it *is* the entry point,
so there is no second binary to hand a token to. A model is a required argument
on every spawn, by the same ticket, and both paths enforce it: nothing here
silently inherits the launching session's default model or falls back to sonnet
without saying so.

**There is one spawner, not two resolving against each other on `PATH`**
(task-42qot). `parlay spawn` runs `tools/cli/internal/spawn` **in-process** —
the former `tools/parlay-bin` module, folded into `tools/cli`. There is no
`parlay-bin` binary and no spawner-resolution order; `resolveSpawner()` is
deleted. `PARLAY_SPAWN_IMPL` (env) or `spawnImpl` (`~/.parlay/config.toml`)
still selects: unset or `"go"` is the in-process path, `"bash"` execs
`parlay-spawn` from `PATH` with `PARLAY_SPAWN_VIA_CLI=1` and passes its exit
code through verbatim, anything else is a usage error. That `bash` arm is a
one-release escape hatch scheduled for deletion; `bin/parlay-spawn` and the
parity suite go with it.

The Go path carries the mandatory-model gate (task-21d36, backported in #238)
and every §2 organ of [`docs/scope-go-spawn.md`](scope-go-spawn.md), plus the
surface bash never grew (`--ephemeral` identities, `<id>=<repo>` batch
dispatch, `--worktree` isolation, `--account`). It does **not** carry the
`PARLAY_SPAWN_VIA_CLI` handshake — deliberately, since it is itself the front
door and has no second binary to hand a token to. Bash keeps its own check for
as long as it exists. `bin/parlay-spawn-parity.test.sh` holds the two sides to
the same gate-chain exit codes and messages, with the `VIA_CLI` scenario
bash-only for that reason.

Post-launch, an optional watchdog (`PARLAY_SPAWN_NO_WATCHDOG=1` to disable)
confirms the spawned agent's first turn actually fired within
`PARLAY_SPAWN_LIVENESS_TIMEOUT_MS` (default 60s), using the same liveness
primitive `monitor.go`'s `watchdog.go` exposes — see [`docs/monitor.md`](monitor.md).

**Env vars** are documented in [`examples/env.example`](../examples/env.example); this doc does not re-list them.
