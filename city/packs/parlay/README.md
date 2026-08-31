# The parlay pack

A Gas City pack (Pack Spec 2.0, schema 2) carrying parlay's contribution to a
city. It is imported by the parlay city's root `pack.toml` under the binding
`parlay`, and is written to stay importable into *any* city — nothing in here
may assume this repository's city.toml or the names of consuming rigs.

A plugin **is** a pack (grill verdict Q2b): this directory is also the
template for what a parlay plugin looks like.

## What lives where, and how `gc` consumes it

| Path | What goes there | How `gc` consumes it (pack-spec §) |
|---|---|---|
| `pack.toml` | Pack identity and metadata only | Read first; `[pack]` validated, imports resolved (§1.2, §2) |
| `agents/<name>/` | One directory per agent: optional `agent.toml` + `prompt.template.md` | Directory name = agent name; stamped `parlay.<name>` via the import binding (§1.2.4, §2.5) |
| `formulas/` | Workflow definitions, canonical `<name>.toml` | Collected as a formula layer; highest-priority layer wins per name (§1.2.8, §2.9) |
| `orders/<name>.toml` | Scheduled/recurring work definitions | Scanned by the orders subsystem (§1.3.5) |
| `commands/<path>/run.sh` | Pack commands; nested dirs = nested command words | Scanned by convention; optional `command.toml` overrides (§1.2.11) |
| `doctor/<name>/run.sh` | Health checks for `gc doctor`; optional `fix.sh`, `doctor.toml` | Scanned by convention (§1.2.10) |
| `assets/` | Private support files (scripts, prompt fragments, overlays) | Never scanned; effective only when a definition references them (§1.3.1) |

Do not invent additional top-level directories — the pack-format namespace is
reserved (§1.1). Private files go under `assets/`.

## Who fills each directory in

This is a skeleton by design; breadth comes from the epic's later children,
each landing in an existing location with no restructuring:

- **`agents/`** — the spawn seam, task-4cfpv.9 (Gas City session runtime
  providers). Nothing may be added here before it lands: spawn is the sole
  creator of the agent record (contract §6).
- **`formulas/`** — mapping stubs with pointers live in
  [`formulas/README.md`](formulas/README.md); real formulas follow the seams
  that need them.
- **`orders/`** — whichever seam first needs scheduled work (candidates:
  liveness patrol cadence, task-4cfpv.10; event-spool maintenance,
  task-4cfpv.11).
- **`doctor/`** — one real check ships now (`doctor/parlay-cli/`: the parlay
  CLI is reachable); seams add checks beside it.
- **`commands/`** — empty until a seam has a real pack command to add.

## Rules inherited from the binding contract

- `requires_gc` stays omitted (parsed, never enforced — contract §4).
- Nothing in this pack may assume a bead backend until Q4 (task-4cfpv.20)
  is ruled.
- Control verbs shell out to `gc` with each verb's own declared JSON flag;
  liveness and event streams use the typed `/v0` HTTP+SSE API (HYBRID mode,
  contract §5).
