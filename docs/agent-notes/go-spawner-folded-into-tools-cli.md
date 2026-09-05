# The Go spawner lives in `tools/cli/internal/spawn` — there is no second spawner binary

`tools/parlay-bin` no longer exists. Its whole tree was folded into `tools/cli`
as `internal/spawn` (plus `internal/juggle`) by task-42qot; its `go.mod`/`go.sum`
were deleted because `tools/cli` already carried every dependency. `parlay spawn`
now runs that code **in-process** — no exec, no second binary, no `PATH`
resolution order, and no `PARLAY_SPAWN_VIA_CLI` handshake on the Go side (the
Go path *is* the front door, so there is nothing to hand a token to; bash keeps
its own check, and the escape hatch sets it).

Dispatch is `config.SpawnImpl()` in `tools/cli/internal/commands/spawn.go`:

| `PARLAY_SPAWN_IMPL` / `spawnImpl` config key | Behavior |
|---|---|
| unset or `go` | in-process `internal/spawn` (the default) |
| `bash` | exec `parlay-spawn` from `PATH` with `PARLAY_SPAWN_VIA_CLI=1`, exit codes passed through verbatim |
| anything else | usage error |

The `bash` arm is a deliberate escape hatch, kept for one release so a
regression in the Go path has a same-day fallback. **PR B deletes it** along
with `bin/parlay-spawn`, its test scripts, and the parity suite — see
[`docs/scope-go-spawn.md`](../scope-go-spawn.md)'s task-42qot addendum.

## What this note used to say, and why it is wrong now

Until task-42qot this note described `tools/parlay-bin` as a dead,
behind-schedule partial port that was never installed on `PATH`, and warned
that `resolveSpawner()`'s preference for it was not evidence the Go path ran in
production. All of that is obsolete: `resolveSpawner` and the whole
spawner-resolution machinery are deleted, and the Go path is what every
`parlay spawn` executes. The organ-by-organ gap matrix that motivated the
reconciliation is still worth reading as the record of what had to be closed —
[`docs/scope-go-spawn.md`](../scope-go-spawn.md) §2 — but read it as history,
not as a live list of gaps.

## Things the move did not change

- ~~**The bash spawner is still byte-compatible where it counts.**~~ Obsolete:
  `bin/parlay-spawn` and its `PARLAY_SPAWN_IMPL=bash` escape hatch were deleted
  in task-42qot PR B1 (#273), and the parity harness went with them. There is
  no second spawner and nothing to diff against — a `spawnImpl = "bash"` line
  left in `~/.parlay/config.toml` is now an inert unknown key, not a fallback.
- **`bin/parlay` must build with `CGO_ENABLED=0`** — folding `internal/spawn`
  into `tools/cli` put the beads dependency's embedded-Dolt/ICU tree in the
  spawner's build graph too (robots-wgij).
- **The launch templates moved with the code** to
  `tools/cli/internal/spawn/launch-templates/`; the repo-root
  `launch-templates/*` symlinks were retargeted, not replaced. See
  [`startup-prompt-template-is-single-source.md`](startup-prompt-template-is-single-source.md).

## What `herdr agent start`'s trailing `-- ARGS` actually mean (task-p5wvc, P0)

`--kind` names a **canonical executable**, and everything after `--` is
**appended to that executable's argv**. It is not a command to run in its
place, and herdr's success condition is that it *detected that expected agent*
in the pane and it is ready for input.

The Go port read `-- ARGS` as "the command to run" and sent
`-- bash -lc '<launchScript>'`. herdr therefore ran `claude bash -lc
'<script>'` — claude with three garbage positional args. The script never
executed, claude never came up healthy, and the only symptom herdr can emit
for that is `{"error":{"code":"timeout","message":"timed out waiting for agent
startup"}}`. Every real launch failed; a unit-tested, green package shipped a
spawner that could not spawn.

Two rules fall out, both now pinned by tests in `spawnpipeline_test.go`:

1. **The trailing argv is flags for the agent's own executable, never a wrapper
   process.** `TestSpawnOneHerdrNeverWrapsTheAgentInAShell` rejects an
   interpreter or a bare word in the first position. The charter is not an
   argument at all — herdr refuses to encode its newlines, so `AgentPrompt`
   delivers it through the paste-safe channel after the start succeeds.
2. **Pane prep runs on every herdr launch, not just `--pane`.** Deleting the
   `bash -lc` wrapper also deleted the `unset` that led its script, and a fresh
   tab inherits the Claude Code nesting markers from the herdr daemon's own
   env. Claude Code refuses to nest and exits at once — which surfaces as the
   same startup timeout. The prep's `READY_$$` marker is separately
   load-bearing: `agent start` rejects a pane that is not "an available shell",
   and only a real shell expands `$$`, so matching the digits form proves the
   line *executed* rather than merely having been typed in.
   `TestSpawnOneHerdrPreparesEveryPaneNotJustBorrowedOnes` pins both.

The general lesson is the one that cost the most here: **a spawner's tests can
only prove the argv it builds, never that the launcher accepts it.** Both
defects were invisible to a green package and visible in one real spawn.
`docs/scope-go-spawn.md` §5 item 1 actively recommended the wrapper design for
two releases before anyone launched an agent through it.
