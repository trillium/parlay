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

- **The bash spawner is still byte-compatible where it counts.** The parity
  harness (`bin/parlay-spawn-parity.test.sh`) still runs both sides through the
  gate chain; its Go side is now `bin/parlay spawn`, and the `PARLAY_SPAWN_VIA_CLI`
  scenario is bash-only because the Go side no longer has that gate.
- **`bin/parlay` must build with `CGO_ENABLED=0`** — folding `internal/spawn`
  into `tools/cli` put the beads dependency's embedded-Dolt/ICU tree in the
  spawner's build graph too (robots-wgij).
- **The launch templates moved with the code** to
  `tools/cli/internal/spawn/launch-templates/`; the repo-root
  `launch-templates/*` symlinks were retargeted, not replaced. See
  [`startup-prompt-template-is-single-source.md`](startup-prompt-template-is-single-source.md).
