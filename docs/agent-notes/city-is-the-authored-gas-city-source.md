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

**Validation recipe that actually works:** build the pinned ref (`third_party/gascity/PIN`;
`tools/gc-build/build-gc.sh`, or contract §1's four ICU env vars /
`CGO_ENABLED=0`), copy `city/` to a scratch dir, `GC_HOME=<scratch>` and run
read-only config verbs there. The `gc` binaries already on `PATH` are the
wrong artifact **for authoring questions** (contract §2) — but they are the
right artifact for one question the pinned ref cannot answer: *does this city
still load on the binary that runs `parlay spawn`?* Validate `gc config show`
against **both**, because they disagree (robots-wdbvx — see below).

**The scaffold imports no builtin packs, on purpose** (robots-wdbvx; details in
`city/README.md`, "Builtin packs"). It used to declare `[imports.core]` and
`[imports.bd]` versionless, on the task-u4uc6 reasoning that a versionless
bundled source resolves the *running* binary's embedded pin offline. That
holds at the pinned ref and fails at `0.15.1.trillium`, whose V2 imports fall
through to the git-include cache (`remote include … is not cached`) — so
`config show` exited 1 and every `parlay spawn` mechanic launch died. The
`core` binding is additionally a hard *shadow* error wherever gc's legacy
implicit-import bookkeeping still lists `core`, and those binaries supply core
implicitly anyway. Missing-builtin-import is a *warning* at refs that want the
imports; an unloadable city is an outage. The identity seed still holds: a gc
that reads `.gc/site.toml` composes `name = "parlay"`, `0.15.1.trillium`
composes an empty name and derives a display name elsewhere. Guarded by
`TestAuthoredPackDeclaresNoBuiltinImports` and `TestAuthoredCityOmitsWorkspaceIdentity`
(CI) and `TestScaffoldConfigShowWarningFree` (gated, runs against either binary).
The `core.control-dispatcher` singleton advisory is gc's own core pack warning
about itself — every city importing core gets it; don't author around it.
