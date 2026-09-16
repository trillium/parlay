# A gc city store pins its schema generation — match the bd to the store, not to main

Verified live on the captain's box 2026-09-16 (task-fx1qk / task-plm4e): the
city bead store at `~/.parlay/gascity/city` existed but was **not usable by
the default toolchain**, and the failure is a schema-generation mismatch, not
fresh-vs-missing.

## The rule

- A city store's schema version is **pinned by whatever first bootstrapped it**.
  The pinned gc (9700d9a, beads lib `v1.3.0-rc.2`) **never auto-migrates a
  shared server store**. On first contact it warns
  `refusing to auto-apply N pending schema migrations to a shared server
  database (v59 -> v66): migrating would lock out every co-resident bd client`
  and degrades to bd shell-outs. So the store stays at its creation version
  forever (until someone migrates it with explicit consent).
- The bd gc shells out to must therefore speak the **store's** version. The
  brain binary (`1.2.2+brain`, knows schema up to v56) only matches a store
  brain itself created (at v56, `gc-main-brain-probe.md` §2). Point `PARLAY_BD`
  at the version that matches the live store.

## The live failure (all three observed this run)

| Component | Knows schema | Against v59 store |
|---|---|---|
| brain bd (`~/.local/bin/bd`, 1.2.2+brain) | v56 | `refusing…` `schema version mismatch: database is at v59, binary knows up to v56` (exit 1) |
| pinned gc native lib (beads `v1.3.0-rc.2`) | v66 | degrades to bd shell-outs (WARN `native_store_unavailable`) |
| upstream bd v1.1.0 (`@v1.1.1-0.20260805093327-bf97b73749ac`) | v59 | **works** — `bd list --json` → `[]` exit 0 |

The store was bootstrapped 2026-08-31 by upstream-generation bd v1.1.0
(`.beads/.local_version: 1.1.0`). This is also why the last-ever `bd init` on
it aborts cleanly ("To use the existing database…", exit non-zero) rather than
re-initialising — metadata.json already exists with `dolt_mode: server`.

## The repeatable bootstrap / repair recipe

```sh
# 1. Upstream bd at the store's generation, CGO-free (build-gc.sh's default).
#    For THIS store the generation is v1.1.0:
CGO_ENABLED=0 go install github.com/steveyegge/beads/cmd/bd@v1.1.1-0.20260805093327-bf97b73749ac

# 2. Existing store: confirm it answers BEFORE mutating.
cd "$HOME/.parlay/gascity/city"          # PARLAY_STATE_HOME default ~/.parlay
~/go/bin/bd list --json                  # expect [] and exit 0

# 3. gc owns the city's managed dolt server: run its bootstrap side effect
#    first. Non-zero exit is expected noise (the final embedded-mode ping).
GC_HOME="$HOME/.parlay/gascity/home" \
  <pinned-gc> --city "$HOME/.parlay/gascity/city" beads health

# 4. Join gc's server, never a bd-owned one (skipped automatically when
#    .beads/metadata.json exists — re-init aborts cleanly).
bd init --prefix pa --server \
  --server-port "$(cat .beads/dolt-server.port)" --non-interactive

# 5. Register gc's session bead types in the store's own config.
bd config set types.custom molecule,convoy,message,event,gate,merge-request,agent,role,rig,session,spec,convergence,step

# 6. Settle first contact, then verify a launch clears store creation.
bd list --json
PARLAY_SPAWN_LAUNCHER=gc PARLAY_GC=<pinned-gc> PARLAY_BD=~/go/bin/bd \
  parlay gc-spawn <id> --start-command /bin/sh -- -c 'sleep 60'
```

Verified outcome 2026-09-16: `gc session new parlay.gcfix-boot` (inert
`/bin/sh -c 'sleep 60'`) created `pa-wisp-t1g` and `session close` returned
`state: closed` — full pass through bead-store creation with the pinned gc +
`PARLAY_BD=~/go/bin/bd`.

## Follow-ups recorded

- `docs/gc-prerequisite.md` previously implied brain works for every city; it
  holds only for a store brain itself created (per `gc-main-brain-probe.md`).
  This note reconciles the two.
- The live box also carries a stale gc on PATH (`0.15.1.trillium` fork, no
  typed-JSON contract) and `tools/gc-build/dist/gc` was absent; a rebuilt pin
  gc lives there now. `PARLAY_GC`/`PARLAY_BD` override the PATH lookup.
- A fresh brain-v56 store would let the default (no-env) launcher work without
  `PARLAY_BD`; recreating the live store that way destroys its v59 database —
  a captain decision, not made here (the live store was `[]` empty anyway).