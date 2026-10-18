# The parlay city — authored Gas City source

This directory is the authored source for parlay's Gas City deployment: the
`city.toml` deployment layer, the root city pack (`pack.toml`), and the parlay
pack (`packs/parlay/`). It is the artifact that makes parlay a Gas City *city*
rather than a competing orchestrator (epic task-4cfpv, child task-4cfpv.8).

It is **source, not a live city**. There is no `.gc/` state directory here and
none may ever be committed. The install unit (P12 of the migration plan in
`docs/gascity-integration-contract.md`, at the repo root) owns turning this source
into a running city; no earlier unit runs `gc supervisor install`.

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
- The `gc` first on `PATH` is a fork build (`0.15.1.trillium`); the pinned
  integration target is the ref in `third_party/gascity/PIN`, built from
  source (contract §2). A pinned-ref build is the right tool for *authoring*
  questions, but it is **not sufficient validation**: `0.15.1.trillium`
  predates the bundled-import resolution the pinned ref has, so a config that
  is clean at the pin can still fail to load in production. Validate against
  **both**, and treat the running binary as the deciding vote (robots-wdbvx).
  Note the captain's interactive shell aliases `gc` to `git commit`; scripts
  are unaffected.

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
asserts it). Validate against **both** gc binaries: the pinned ref the tests
name, and the `gc` the spawn path actually resolves off `PATH`
(`0.15.1.trillium` on this box) — they disagree about builtin imports, and the
running one wins. Two warnings this source used to trip are fixed at source:

- *"workspace identity fields are deprecated in v2; move them to
  .gc/site.toml"* — `workspace.name` is gone from `city.toml`. The authored
  identity (`parlay`) is seeded into `.gc/site.toml` (machine-local, never
  committed) by `cityscaffold.Materialize` when absent; `gc init` at install
  owns it thereafter. A bare copy of `city/` without that seed simply has no
  declared identity (gc falls back to the directory basename) — still
  warning-free. NOTE: only a gc that reads `.gc/site.toml` picks the seed up;
  `0.15.1.trillium` ignores it and derives the name from the directory.
- *"city pack declares [imports.core] which shadows the bootstrap implicit
  import(s) with the same name"* — `pack.toml` no longer declares any builtin
  import. See the next section; this one is fixed by omission, not by
  rewriting.

## Builtin packs (core, bd) are deliberately not imported

`pack.toml` used to declare `[imports.core]` and `[imports.bd]` as versionless
bundled sources, on the task-u4uc6 reasoning that a versionless bundled source
resolves the *running* binary's embedded canonical pin offline
(`config.resolveBundledSourceWithoutLock`). That reasoning holds at the pinned
ref. It does **not** hold at `0.15.1.trillium`, the fork build on `PATH` that
`parlay spawn` resolves — and that binary is the one that runs. Two failures,
both reproduced with the real binary (`robots-wdbvx`):

1. `[imports.bd]` (and `[imports.core]`) never resolve. `0.15.1.trillium`
   predates `resolveBundledSourceWithoutLock`, so a bundled V2 import falls
   through to the git-include cache and dies with `city import "bd": remote
   include https://github.com/gastownhall/gascity.git#main is not cached at
   <city>/.gc/cache/includes/…`. `gc config show` exits 1 and every `gc
   session new` fails on it.
2. `[imports.core]` additionally hard-stops any host whose `GC_HOME` carries
   gc's legacy implicit-import bookkeeping (`implicit-import.toml`), because
   `core` is a *bootstrap-managed* implicit import name there: *"city pack
   declares [imports.core] which shadows the bootstrap implicit import(s) with
   the same name; rename one side"*. Renaming the binding does not help — the
   collision gate compares binding names against gc's bootstrap set, and the
   same binaries that carry that set supply `core` implicitly anyway, so
   declaring it is redundant as well as illegal.

Consequence to keep in view: a gc new enough to resolve bundled V2 imports
(the pinned ref) warns *"does not import required builtin pack(s) core, bd"*
on this city and offers `gc doctor --fix`. That warning is **accepted, not
fixed**, until the running gc is that gc: the two refs cannot both be
satisfied, and a city that fails to load on the binary in production is a
fleet outage while a missing-content warning is not. Re-add both imports (with
`gc doctor --fix`, or by deleting the comment block in `pack.toml`) at the same
time as the gc upgrade, and re-check `TestAuthoredPackDeclaresNoBuiltinImports`
and `TestScaffoldConfigShowWarningFree`.

One further warning **remains expected and is not scaffold-attributable**: the
`core.control-dispatcher` `max_active_sessions=1` singleton advisory comes
from gc's own builtin core pack and fires for every city that imports it
(including a bare `gc init` city). Upstream noise; do not author around it.
The production binary adds one more of its own: *"unknown field
`workspace.suspended_on_start`"* — `0.15.1.trillium` predates that field. The
field is the deliberate safety default `city.toml` documents, it costs exit 0
only, and moving it is the spawn seam's call, not this file's.

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
