# `city/` is parlay's authored Gas City city + pack source, not a live city

**The rule:** top-level `city/` holds the authored source for parlay's Gas City
deployment — `city.toml` (deployment layer), root `pack.toml` (imports the
parlay pack), and `city/packs/parlay/` (the parlay pack, Pack Spec 2.0). It is
consumed at install time by the migration plan's P12 unit. Never run
city-mutating `gc` verbs (`gc start`, `gc supervisor *`, `gc init`) in or
against that directory with the default `GC_HOME`: the Gas City supervisor is a
shared machine-wide singleton (`com.gascity.supervisor`, `127.0.0.1:8372`), and
a start against the default home acts on the captain's running city. Validate
against a *copy* with `GC_HOME` redirected; a `.gc/` directory appearing under
`city/` is a bug.

**Why it exists:** task-4cfpv.8 ("Author parlay city.toml and the parlay pack
skeleton", 2026-08-30) created the tree as the artifact that makes parlay a Gas
City city rather than a competing orchestrator. The layout is deliberately
seam-shaped: each later epic child (spawn → `agents/`, events/liveness →
`city.toml` sections, formulas → `formulas/`) drops content into an existing
well-known location without restructuring.

**Load-bearing choices, so they aren't "fixed" later:**

- `suspended_on_start = true` and `[api] port = 0` are safety defaults, not
  omissions. Spawn seam (task-4cfpv.9) flips suspension; events/liveness seams
  (task-4cfpv.11/.10) own enabling the API. Read before write, observe before
  control (contract §7).
- `[beads]` is absent because Q4 (task-4cfpv.20) is an open captain decision.
  Adding a backend table there is deciding Q4 by default — the exact failure
  the epic warns against.
- `requires_gc` is omitted from both pack manifests on purpose: Gas City
  parses it and never compares it (contract §4). Declaring it manufactures a
  version gate that does not exist.
- Definitions live in `city/packs/parlay/`, not inline in `city.toml`, so the
  pack stays importable-by-binding into any other city (a plugin IS a pack —
  grill Q2b). Imports live in the root `pack.toml`, not `city.toml`, per
  pack-spec: city.toml is the deployment layer.

**Validation recipe that actually works:** build the pinned ref `7c817e064`
per contract §1 (the four ICU env vars, or `CGO_ENABLED=0`), copy `city/` to a
scratch dir, `GC_HOME=<scratch>` and run read-only config verbs there. The
`gc` binaries already on `PATH` are the wrong artifact (contract §2).

**Two validation warnings are expected, not defects** (details in
`city/README.md`): the missing builtin-pack imports (`core`, `bd`) are written
by `gc init` at install with the installing binary's own pins — and `bd` only
under the bd beads provider, which is open Q4 — so committing them would be
both version skew and a Q4 pre-decision; and the `workspace.name` deprecation
is non-fatal migration guidance whose target (`.gc/site.toml`) is a
machine-local install artifact that can't hold the authored identity.
