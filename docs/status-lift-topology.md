# Status-lift topology decision (unit 0)

## Purpose and standing

This document is the **decision record** for how parlay's crew-status representation
reaches a beads store — unit 0 of the status-lift seam (epic bead `task-4cfpv.12`).
It adopts a topology, records the options that were on the table with the evidence
that ruled each in or out, and states the adopted option's costs so later units
inherit them as known facts rather than discoveries.

The evidence base is the scope report delivered as bead `task-4cfpv.24`
(agent `scope-status-lift`, 2026-08-28), verified against:
parlay @ `46862563`, gascity `~/code/gascity` @ `1e5229b6d` (`v1.4.0-511`),
beads `~/code/beads` @ `b99d5123c` (`github.com/steveyegge/beads`).

**Scope.** This document decides only the *transport topology* — which mechanism
parlay code uses to read and write beads. The crew-bead schema is
[`docs/crew-bead-schema.md`](crew-bead-schema.md) (unit 2); the client package
wrapping this topology is `tools/cli/internal/parlaybeads` (unit 1). Nothing here
cuts over any existing reader or writer: today's status-file behavior is unchanged
until units 3–8 land, and those are gated on the spawn and events seams
(report §6.4).

## The decision

**Adopted: topology (d) — import `github.com/steveyegge/beads` directly, opening a
store at a parlay-controlled `beadsDir` path, with an events append alongside.**

The target this satisfies is `VISION.md:13`: *"crew state and lifecycle live in the
relay, backed by a Gas City bead store (`GC_BEADS`) at a parlay-controlled path; no
PAI federation is required."*

## The four options

### (a) Shell out to the `gc` CLI — REJECTED

`cmd/gc/cmd_beads.go` exposes only `list`, `show`, and `health`; `list` filters only
by `--label`/`--status`/`--all`. **Read-only and strictly narrower than the `Store`
interface** — no write surface at all. Worse, the deployed binary skews from source:
the `gc` first on the captain's PATH is `0.15.1.trillium`, whose `gc beads` has no
`list`/`show` subcommand, while every capability claim above is true only of source
months ahead of it (report §7.4). A topology that depends on whichever `gc` is on
PATH inherits that skew forever.

### (b) Shell out to `gc bd` — REJECTED

`cmd/gc/cmd_bd.go` is a flag-passthrough to the **`bd` binary**. It is the only
bead-write surface reachable via `gc`, and it does not remove the external-binary
dependency — it presupposes it, and adds a second (`gc`) in front. parlay's status
writers are short-lived CLI processes with no server; every write would fork two
binaries subject to the same PATH-skew problem as (a).

### (c) Gas City HTTP + SSE — REJECTED (for this seam)

`internal/api/supervisor_city_routes.go` is a full typed write surface (`POST
/beads`, `PATCH /bead/{id}`, `POST /events`, SSE with `Last-Event-ID` resume) and
the auth cost is low. But it requires a **running controller**, which `parlay
status` — a bare CLI append that today needs nothing but a writable file — does not
have and must not grow. A status write that fails because a supervisor daemon is
down would be a regression in exactly the degraded situations status reporting
exists for. (The HTTP path remains attractive for *readers* that already assume a
live system; nothing here forbids a later unit adopting it where a controller is
guaranteed.)

### (d) Import `github.com/steveyegge/beads` — ADOPTED

- **It is the same library gascity itself depends on** (`gascity/go.mod:27`), so
  there is no format divergence to manage: the store parlay writes *is* a beads
  database, readable by `bd` and by Gas City.
- **`tools/cli` is its own Go module** (`tools/cli/go.mod:1`) and can take the
  dependency directly; gascity's `internal/beads` is unimportable (Go `internal/`
  visibility — `pkg/` exports only `eventexport`), so the library is the only
  in-process route.
- **`beads.OpenBestAvailable(ctx, beadsDir)` takes an arbitrary path** — a
  parlay-controlled `beadsDir`, no PAI federation, no `gc` binary, no controller:
  `VISION.md:13` satisfied literally.
- **Self-contained writers.** Only (d) keeps `parlay status` a process that needs no
  other running or installed parlay/gascity component.
- **Loud failure is built in.** The library's backend selection fails with named,
  actionable errors rather than silently opening an empty store
  (`beads` `internal/configfile/backend_messages.go`), which is the substrate the
  Q5b contract (refuse loudly, never silently degrade) wraps in unit 1.
- **Immune to version skew.** The behavior is pinned by the module version in
  parlay's own `go.mod`, not by whichever `gc` is on PATH.

## Costs of (d) — stated up front

1. **Dolt.** The library is Dolt-only: `OpenBestAvailable` dispatches to a
   registered extension backend, else Dolt server mode, else embedded Dolt.
   `postgres`/`mysql`/`sqlite` configs are recognized only to fail loudly. Dolt is
   installed on the captain's box (`/opt/homebrew/bin/dolt`); any other deploy
   target inherits the requirement.
2. **CGO or server mode.** Embedded Dolt requires CGO (`beads_cgo.go`); a non-CGO
   build errors at open time with *"embedded Dolt requires CGO; use server mode
   (bd init --server)"* (`beads_nocgo.go`). parlay's build must either accept CGO
   or run the store in server mode. Whether embedded-CGO is acceptable in parlay's
   build is settled by a build experiment in unit 1, not assumed here.
3. **A real dependency tree** lands in `tools/cli/go.mod`. Binary-size and
   build-time growth are measured, not guessed, in unit 1.

## Ruled out on principle: `GC_BEADS=file`

The one seemingly-cheap path is explicitly **not beads at all** (report §9.2):
gascity's `FileStore` persists gascity's own private JSON structure, readable only
by gascity's unimportable `internal/beads` — `bd` cannot read it. Adopting it would
produce a store that is not a beads database, violating the architecture-grill Q5/Q5a
rulings ("bd is still the required backend of this thing"). Do not resurrect it.

## Reversibility

The store under (d) is a directory at a parlay-controlled path that nothing else
references; rollback of any unit up to migration is "stop reading the beads, resume
reading the files" (report §8). The only irreversible step in the seam is unit 8
(deleting replaced storage code), which is sequenced last and is not covered by this
decision.
