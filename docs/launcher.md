# Launcher (spawn)

**Code:** `tools/cli/internal/spawn` (`spawn.go` + `spawnpipeline.go` + `launcher.go` + `watchdog.go`), run in-process by `parlay spawn`.

`parlay spawn <agent-id> <display-name> <hex-color> <initial-prompt> ...`
launches a brand-new, top-level agent session inside a detached
[herdr](https://github.com/trillium/herdr) terminal, registers it in the
[agent registry](agent-registry.md), posts a hello so its tab goes live
immediately, and hands it a startup prompt that arms `parlay listen`/`monitor`
on boot — so a spawned agent is a reachable chat tab from the moment it
starts, not after its own first enrollment call. It explicitly unsets
`CLAUDECODE`/`CLAUDE_CODE_*` before launching, because herdr terminals inherit
the parent environment and Claude Code refuses to nest under those vars.

**There is exactly one spawner** (task-42qot). `parlay spawn` runs
`tools/cli/internal/spawn` in-process. There is no `parlay-bin` binary, no
`bin/parlay-spawn` script, no `PARLAY_SPAWN_IMPL` selector, and no
spawner-resolution order — all four were deleted, in that order, across PR A
and PR B. The `PARLAY_SPAWN_VIA_CLI` handshake went with them: it existed to
prove a call came through the CLI rather than straight into the script, and
with no script there is no second front door to police.

Every spawn requires a *resolved* model (task-qyu8q), but the model need not be
passed explicitly. It may come from `--model`, from a `--profile` that carries
one, or from `--no-pii`'s free-model auto-routing, each of which resolves
before the `requireModel` gate runs. What no spawn does is silently inherit the
launching session's default model or fall back to sonnet without saying so.

## Launching under herdr

`herdr agent start <id> --kind <kind> --pane <p> -- <args>` types the kind's
canonical executable plus `<args>` into the pane as a shell command line, so
`--kind` decides what actually launches. claude gets the YOLO flag set
(skip-permissions, sonnet fallback, `--strict-mcp-config`, posthog disabled) so
a remotely driven agent never stalls on a permission prompt the absent user
cannot answer; every other harness gets only an explicit `--model` and relies
on its own config (opencode's permission surface is its `opencode.json`).

The charter is delivered separately, by `herdr agent prompt`. It cannot ride in
the `agent start` argv: the charter is multi-line and herdr refuses to encode a
newline into a shell command line ("agent arguments cannot be encoded safely
for the target shell"). A charter that fails to land rolls the tab back, the
same as a failed start — a started agent with no task is worse than no tab.

## Post-launch watchdog

Every launcher gets one, armed as a DETACHED `parlay spawn-watchdog` child so
it outlives the `parlay spawn` process that started it (a goroutine cannot: the
CLI exits within milliseconds of arming).

| Launcher | What the watchdog does |
|---|---|
| `herdr` | Waits for the agent to reach `working`; if the first turn never fired, re-sends the charter (a never-started agent has done no work, so a resend duplicates nothing) and confirms via `verify-send` when it is on PATH. |
| `subprocess` | Polls `/api/chat/subscribers` for the agent's own poll channel. Observation only, never a re-prompt: the charter went to the child's stdin once and there is no second delivery channel. |
| `gc` | Delegates confirm-or-report to `parlay gc-liveness` and appends its `--json` envelope to `<agent-dir>/charter-delivery`. |

`PARLAY_SPAWN_NO_WATCHDOG=1` disables arming;
`PARLAY_SPAWN_LIVENESS_TIMEOUT_MS` (default 60000) tunes the window. Each arm
logs to `$TMPDIR/parlay-watchdog-<launcher>.log`.

**Env vars** are documented in [`examples/env.example`](../examples/env.example); this doc does not re-list them.
