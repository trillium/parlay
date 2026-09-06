# `tools/cli/internal/spawn` is the only spawner there is

`parlay spawn` runs the spawn pipeline **in-process**. There is no second
implementation, and no way to reach one:

| Gone | What it was | Removed by |
|---|---|---|
| `tools/parlay-bin` | a separate Go module + binary, preferred by name on `PATH` | PR A (task-42qot) |
| `resolveSpawner()` / `resolveSpawnerChoice` | the precedence ladder that picked between them | PR A |
| `bin/parlay-spawn` | the original 1859-line bash spawner | PR B |
| `PARLAY_SPAWN_IMPL` / `spawnImpl` | the `go`-vs-`bash` selector | PR B |
| `PARLAY_SPAWN_VIA_CLI` | the handshake proving a call came through the CLI | PR B |

The handshake went with the script it guarded. It existed so
`bin/parlay-spawn` could refuse a direct invocation that bypassed `parlay
spawn`'s gates; with one implementation behind one verb, the property is
structural and a token would guard nothing.

**Practical consequence:** anything that used to exec `parlay-spawn` now runs
`parlay spawn`. Three callers were migrated in PR B —
`tools/mechanic-dispatch/mechanic-dispatch`, `identity --launch`, and `parlay
variant launch`. The two Go callers re-exec **this** binary via
`os.Executable()` rather than looking for `parlay` on `PATH`; under `go test`
that resolves to the test binary, which is why `identity`'s dispatch is a
package var a test can point at a stub.

## Read `docs/scope-go-spawn.md` as history

Its §2 organ-by-organ gap matrix is the record of what the reconciliation had
to close, and §1 is the only surviving inventory of what the bash spawner did.
Neither is a live map: every path it cites as `tools/parlay-bin/<file>.go` is
`tools/cli/internal/spawn/<file>.go`, and its Stage 3–5 end-state (two
implementations resolving against each other, bash as the permanent escape
hatch) is superseded by this note.

The bash script itself is recoverable — `git show 046919aa:bin/parlay-spawn` —
and the `bin/parlay-spawn:<line>` citations throughout `internal/spawn` point
into that blob, deliberately. They are provenance for why a ported behavior is
shaped the way it is; do not re-derive them, and do not read them as live
paths.

## Things the fold did not change

- **`bin/parlay` must build with `CGO_ENABLED=0`** — folding `internal/spawn`
  into `tools/cli` put the beads dependency's embedded-Dolt/ICU tree in the
  spawner's build graph too (robots-wgij).
- **The launch templates moved with the code** to
  `tools/cli/internal/spawn/launch-templates/`; the repo-root
  `launch-templates/*` symlinks were retargeted, not replaced. See
  [`startup-prompt-template-is-single-source.md`](startup-prompt-template-is-single-source.md).
- **The bash-vs-Go parity harness is gone**, deleted with the bash side it
  diffed against. What replaced it is ordinary Go test coverage in
  `internal/spawn` — which means a behavior nobody wrote a Go test for is now
  unguarded, where the A/B used to catch it. Treat `internal/spawn` changes
  accordingly.
