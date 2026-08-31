# The parlay city — authored Gas City source

This directory is the authored source for parlay's Gas City deployment: the
`city.toml` deployment layer, the root city pack (`pack.toml`), and the parlay
pack (`packs/parlay/`). It is the artifact that makes parlay a Gas City *city*
rather than a competing orchestrator (epic task-4cfpv, child task-4cfpv.8).

It is **source, not a live city**. There is no `.gc/` state directory here and
none may ever be committed. The install unit (P12 of the migration plan in
[`docs/gascity-integration-contract.md`](../docs/gascity-integration-contract.md))
owns turning this source into a running city; no earlier unit runs
`gc supervisor install`.

## Safety — read before running any `gc` verb near this directory

- **The Gas City supervisor is a shared, machine-wide singleton** (launchd
  label `com.gascity.supervisor`, `127.0.0.1:8372`). `gc start`, `gc supervisor
  stop/reload` against the default home act on the captain's running city with
  the mayor session in it — same hazard class as a broad `pkill`. **Every
  experiment must redirect `GC_HOME` and the supervisor port**, and should run
  against a *copy* of this directory, never in place (a stray `.gc/` here is a
  bug).
- Belt and braces: `city.toml` sets `suspended_on_start = true`, so even a
  mistakenly started city spawns no agents until the spawn seam
  (task-4cfpv.9) deliberately flips it.
- The `gc` first on `PATH` is a stale fork build (`0.15.1.trillium`); the
  integration target is the pinned ref `7c817e064` built from source
  (contract §2). Validate config changes against a pinned-ref build, not the
  installed binary. Note the captain's interactive shell aliases `gc` to
  `git commit`; scripts are unaffected.

## How `gc` consumes this tree

Per Pack Specification 2.0 (`docs/reference/specs/pack-spec.md` at the pinned
Gas City ref, readable in the local clone `~/code/gascity`):

1. `gc` loads `city.toml` from the city root — the deployment layer
   (workspace identity, runtime toggles, backend config).
2. The sibling `pack.toml` is the **root city pack**. Its
   `[imports.parlay]` binding pulls in `packs/parlay/`, and the loader stamps
   the binding on everything the import contributes: an agent `X` in
   `packs/parlay/agents/X/` gets runtime name `parlay.X`.
3. Within `packs/parlay/`, well-known directories (`agents/`, `formulas/`,
   `orders/`, `commands/`, `doctor/`) are discovered by the loader's
   conventional rules — see [`packs/parlay/README.md`](packs/parlay/README.md).

## Validating changes to this tree

Build the pinned ref per contract §1 (`tools/gc-build/build-gc.sh`), copy
`city/` to a scratch directory, redirect `GC_HOME` (and `HOME`) there, and run
read-only config verbs — `gc config show` resolving with exit 0 and **no
scaffold-attributable warnings** is the bar (task-u4uc6; the gated test
`TestScaffoldConfigShowWarningFree` in `tools/cli/internal/cityscaffold`
asserts it). Two warnings this source used to trip are fixed at source:

- *"does not import required builtin pack(s) core, bd"* — `pack.toml` now
  declares `[imports.core]` and `[imports.bd]` with **no `version` pin**. A
  versionless bundled source resolves to the *running* binary's embedded
  canonical pin, offline, so there is no skew against whatever binary P12
  installs (a committed sha would degrade to a network-only import once gc's
  pin moves). The `bd` import matches the provider default ("bd" when no
  `[beads]` table is declared); it follows Q4 (task-4cfpv.20) if that ruling
  changes the provider.
- *"workspace identity fields are deprecated in v2; move them to
  .gc/site.toml"* — `workspace.name` is gone from `city.toml`. The authored
  identity (`parlay`) is seeded into `.gc/site.toml` (machine-local, never
  committed) by `cityscaffold.Materialize` when absent; `gc init` at install
  owns it thereafter. A bare copy of `city/` without that seed simply has no
  declared identity (gc falls back to the directory basename) — still
  warning-free.

One warning **remains expected and is not scaffold-attributable**: the
`core.control-dispatcher` `max_active_sessions=1` singleton advisory comes
from gc's own builtin core pack and fires for every city that imports it
(including a bare `gc init` city). Upstream noise; do not author around it.

## What is deliberately absent, and who owns filling it in

| Absent | Owner | Why absent now |
|---|---|---|
| `[beads]` backend | task-4cfpv.20 (Q4) | Substrate is an open captain decision; no seam may assume a backend |
| Providers, agents, session config | task-4cfpv.9 (spawn seam) | Spawn owns launch-spec→template synthesis and the agent record |
| `[events]`, enabled `[api]` | task-4cfpv.11 (events seam) | Events seam owns recorder, SSE contract, event-name registry |
| Health-patrol/daemon tuning | task-4cfpv.10 (liveness seam) | Liveness oracle moves behind a shadow flag first |
| Real formulas | later epic children | Mapping stubs with pointers live in [`packs/parlay/formulas/README.md`](packs/parlay/formulas/README.md) |

Each lands in an existing well-known location — dropping real content in must
not require restructuring this tree.

## Version note

`requires_gc` is **deliberately omitted** from both pack manifests: Gas City
parses and preserves it but never compares it (contract §4 — the field is a
trap that looks like a working version gate). Any version floor parlay needs
is parlay's own named-error check, not this field.
