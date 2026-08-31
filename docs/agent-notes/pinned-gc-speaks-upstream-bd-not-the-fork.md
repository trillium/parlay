# The pinned gc speaks upstream bd — the captain's bd fork cannot back a gc city

Discovered while landing spawn-lift unit 4 (epic task-4cfpv.9), building the
gated integration test that proves `gc session new` starts a session from a
parlay-synthesised template.

## The incompatibility

The pinned Gas City (`third_party/gascity/PIN`) vendors
`github.com/steveyegge/beads` as a native Go library (see the version in the
pin's `go.mod`) **and** shells out to whatever `bd` binary is on PATH for
bead creation. Both halves assume the upstream beads store schema and config
dialect.

The captain's installed `bd` (`~/.local/bin/bd`, version `1.2.2+brain.`) is a
diverged fork. Concrete failures observed when it backs a gc city store:

- A store created by the fork lacks columns the pin's vendored library
  queries — `gc session new` dies with
  `Error 1105: column "row_lock" could not be found`.
- A store bootstrapped by gc natively is refused by the fork's CLI —
  `bd create` demands `issue_prefix` config the native bootstrap never
  writes.

There is no configuration that makes the two agree; the fix is to use an
upstream `bd` for anything gc-owned:

```
CGO_ENABLED=0 go install github.com/steveyegge/beads/cmd/bd@<version in the pin's go.mod>
```

A CGO-free build cannot open embedded Dolt stores — and **do not reach for
`bd init --proxied-server`**: gc's beads component (`gc-beads-bd`) owns the
city store's `dolt sql-server`. It starts a managed server on an OS-assigned
port serving `.beads/dolt` and records the port in `.beads/dolt-server.port`;
if a bd-owned proxied server made the store first, gc's managed server later
adopts the recorded port (after bd's dolt idles out at 30s) and every
subsequent bd proxy respawn times out against the foreign listener
(`timeout waiting for proxy to become ready on its OS-assigned port`) —
deterministically, both from bd directly and from every `bd` shell-out
inside `gc session new`.

The working order (proven end-to-end):

1. `gc beads health` on the city **before any bd init** — run it purely for
   its bootstrap side effect: it starts the managed dolt and writes
   `.beads/{config.yaml,dolt-server.port}`. It still exits 1 with a CGO-free
   bd on PATH ("recovered but store not ready" — the probe ends with an
   embedded-mode ping bd cannot answer); tolerate that.
2. `bd init --prefix pa --server --server-port $(cat .beads/dolt-server.port)
   --non-interactive` — join gc's server ("pa" matches the `issue_prefix`
   gc's bootstrap wrote). This writes `.beads/metadata.json` with
   `dolt_mode: server`.
3. `bd config set types.custom molecule,convoy,message,event,gate,merge-request,agent,role,rig,session,spec,convergence,step`
   — gc's session beads need its custom types registered in the store's own
   config; the `types.custom` line gc writes into `.beads/config.yaml` is
   NOT what create-validation reads, so without this `session new` dies with
   `invalid issue type: session`.
4. One `bd list` to settle first contact, then `gc session new` works.

## Sandbox lifecycle traps (all hit for real)

- **The managed-Dolt scope watchdog outlives its city dir by minutes** (it
  polls coarsely). A test must reap leftovers itself: sweep processes whose
  full command line contains the unique temp city path — never a
  name-pattern kill.
- **Sessions are tmux; the socket is named after the city directory**
  (`tmux -L <basename>`), so a scaffold city always lands on socket `city`,
  shared per-user across every test run. Kill only the exact session name
  returned by `session new --json`.
- `gc init` requires `lsof`, which lives in `/usr/sbin` — a dir often
  missing from sandboxed PATHs.

The full working recipe is executable:
`tools/cli/internal/gctemplate/integration_test.go`
(`TestSynthesisedTemplateStartsSession`, gated on `PARLAY_GC_INTEGRATION=1`
+ `PARLAY_GC` + `PARLAY_BD`).

## Consequence for units 5–8 (spawn-path wiring)

Shipping gc as a runtime prerequisite is not enough for the launch seam: a
city that parlay operates needs an **upstream-compatible bd** on the PATH gc
sees, or every `session new` fails at bead creation. Whatever wires
`PARLAY_SPAWN_LAUNCHER=gc` must decide where that bd comes from (vendor it
like gc, or extend `parlay doctor`'s gc check to probe the bd on PATH) —
`docs/gc-prerequisite.md` documents the gc half only.
