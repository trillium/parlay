# Record staleness: model, termination, and cost

Design record for `tools/cli/internal/staleness`, the representation-plane
record-staleness engine (issue #128 §21–§24, §57; epic child task-4cfpv.14).

**Scope disambiguation first:** this is staleness of *records* (beads —
work products, workflow versions, architecture beads). It is unrelated to
the agent/worktree staleness handled by `parlay stale` and `parlay sweep`
(execution-plane liveness). The two share a word and nothing else, and the
packages must never entangle.

## The problem #128 leaves open

#128 §22 says staleness propagates along explicit dependency edges, and
§23 says a read must not create such an edge. It supplies **no termination
guarantee and no cost bound**. A naive push implementation ("when X
changes, walk dependents and mark them stale, recursively") is unbounded:
cycles loop forever, diamonds multiply work, and a hot upstream record
turns every edit into a graph-wide write storm.

## Adopted prior art: Dagster's version pairing

Considered:

- **Dagster** pairs each asset with a *data version* and records, per
  dependency, the upstream data version the asset was last materialized
  against. Staleness is **derived** by comparing recorded versions to
  current versions — nothing is eagerly cascaded.
- **Bazel / Nix / Buck2** use content-addressed invalidation: a node's key
  hashes its inputs' keys; unchanged hashes cut propagation off early, and
  DAG-once evaluation bounds the work.

**We adopt the Dagster model.** Rationale:

1. Beads are not content-addressed artifacts, and hashing record content
   would drag the engine into interpreting record semantics — exactly what
   #128 §86 forbids Parlay from owning. Versions, by contrast, are already
   first-class in #128 (§14 workflow versions, §17 SemVer severity).
2. Derived staleness makes unbounded propagation **structurally
   impossible**: a change is O(1) at write time because there is no
   propagation step to bound. The question "what stops the cascade?" is
   answered by "there is no cascade."
3. Bazel's most valuable property — **early cutoff** — falls out as a
   corollary (see below), so we lose nothing by not content-addressing.

## The model

Every record carries an opaque `Version` (compared for equality only —
the engine never orders, parses, or interprets it). Every **declared**
dependency edge stores the upstream version the dependent last validated
against (the *acked* version).

- **Directly stale**: some edge's acked version ≠ that upstream's current
  version.
- **Transitively stale**: directly stale, or some record reachable over
  declared dependency edges is directly stale. This is a plain
  reachability query.
- **Reads** (`RecordRead`) are provenance-only attachments. No staleness
  computation ever follows them — the §23 guarantee is enforced by
  construction, not by convention.
- **Revalidate** re-acks a record's edges at current upstream versions
  *without* moving the record's own version. If revalidation actually
  produced a new output, the caller bumps the version explicitly.

### Early cutoff

Because `Revalidate` does not move the record's own version, a record
that re-checks against a changed upstream and produces the same output
shields its entire downstream subgraph: its dependents' acked versions
still match. A cascade dies at the first node whose output did not
change — the same property Bazel gets from content hashing, obtained
here from version pairing.

## Termination

There is no propagation process to terminate at write time (`Bump` is
O(1), touches one record). Every query that walks the graph uses a
visited set and visits each record **at most once per call**:

- Transitive staleness = reachability to a directly-stale node; visited
  set ⇒ terminates on cycles, does not re-walk diamonds.
- Affected-set enumeration (reverse walk from a changed record) is the
  same, plus a hard node budget.

Termination therefore does not depend on the graph being acyclic. Cycles
are permitted at declaration time (only self-edges are rejected) because
the traversals, not the writers, own termination.

## Cost model

| Operation | Cost | Bound mechanism |
|---|---|---|
| `Bump` (a change lands) | O(1) | no traversal exists at write time |
| `DirectlyStale` | O(out-degree) | compares each declared edge once |
| `IsStale` (transitive) | O(V+E) of reachable subgraph, worst case | visited-once traversal |
| `Why` (explanation) | O(V+E) of reachable subgraph, worst case | visited-once traversal |
| `Affected` (one propagation pass) | ≤ node budget | visited-once + hard budget, truncation reported |

**What stops a cascade** (defense in depth):

1. Derived staleness — no eager marking, so a change cannot fan out.
2. Visited-once traversal — cycles and diamonds cannot multiply work.
3. Early cutoff — an unchanged output shields everything downstream.
4. The `Affected` node budget — the only cascade-shaped operation carries
   a hard cap and reports truncation instead of running on.

## Observability

`Why(id)` answers "why is this record stale, and what edge made it so":
it returns every directly-stale edge reachable from the record over
declared dependencies, each with the dependency path from the queried
record, the acked and current upstream versions, and the reason string of
the upstream's last bump. Reads never appear in an explanation, because
they are never walked.

## Supersession seam (task-4cfpv.13)

A major supersession is a natural staleness source, but **severity policy
lives in the supersession module, not here**. The seam is one call:

> When the supersession policy decides a supersession is
> staleness-inducing (per its SemVer severity rules, #128 §17), it calls
> `Bump(supersededID, newVersion, "superseded:<severity>")`.

The staleness engine consumes only version moves and stays
severity-agnostic; the reason string flows through to `Why` explanations
untouched. Nothing here blocks on the supersession work landing — any
caller that moves versions gets identical behavior.

## What this package deliberately is not

- Not a store: in-memory, no I/O; persistence and wiring into beads is a
  later unit.
- Not a policy engine: what to *do* about a stale record (rerun, review,
  escalate, queue) is workflow-defined per #128 §21–§22.
- Not the agent/worktree staleness of `parlay stale`/`sweep`.
