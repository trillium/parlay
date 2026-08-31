# Supersession with SemVer-classified migration severity

Deliverable of task-4cfpv.13 (epic child of issue #128 §13–§19, §77,
§100–§101). Engine: `tools/cli/internal/supersession` — pure, deterministic,
no I/O, no clock, no inference, following the `internal/routing` and
`internal/staleness` pattern.

This is the parlay-owned policy that
[`gascity-plane-boundary.md`](gascity-plane-boundary.md) register row 4
named **open**: Gas City (`gc formula version-check`) surfaces the *fact* of
drift; what parlay does about a changed definition — migrate, supersede,
reprocess, and at what severity — is parlay's to define. This document
defines it.

## The model in one paragraph

A versioned definition (a workflow version, a source contract version, any
reusable definition — the kind vocabulary is open, #128 §25) is an immutable
**record**. Records are **superseded, never mutated** (#128 §111.5): a new
record names the record it supersedes, both remain in the ledger forever,
and the **head** of a chain is the one record nothing supersedes. Every
supersession declares a strict SemVer bump and a **changeset** in a closed
mechanical vocabulary; the bump is validated against the changeset's proven
severity floor, and the resulting severity mandates a **reprocessing
requirement** that stays pending until an actor resolves it with evidence.
Everything is a fold over an append-only event log, so "why was this record
superseded and what did it trigger" (`Explain`) is always answerable.

## Record chains

- One chain per logical `Name`; each version is its own record with its own
  ID (#128 §14), so existing work keeps an exact reference to the version
  that governed it.
- Chains are **linear**. Superseding anything but the current head is a
  *stale supersede*, rejected with the real head named. (#128 §13 shows
  v2→supersedes→v1; a branching chain would make "the head" plural and head
  resolution non-deterministic. Chosen default — #128 leaves this open.)
- A supersession may not re-identify the definition: `Name` and `Kind` are
  fixed for the chain's life.
- History is never destroyed (#128 §15, §79): `History(name)` returns every
  version, root first.

## Severity: concrete rules, not vibes

Two independent inputs, held against each other:

**1. The declared bump** (`BumpKind`) — the author's claim, from the version
step. Strict numeric `MAJOR.MINOR.PATCH` only (no prerelease/build/`v`).
Skips allowed; lower fields must reset (`1.2.3 → 2.1.0` is rejected).

**2. The classified floor** (`Classify`) — what the changeset proves. Each
change carries one class from a closed vocabulary (unknown classes are
rejected — parlay does not guess):

| class | meaning | floor |
|---|---|---|
| `annotation` | consumer-invisible (descriptions, metadata, docs) | patch |
| `additive` | new optional structure; existing consumers unaffected until they opt in | minor |
| `compatible` | behavior refined, declared contract preserved (#128 §17's "optimization") | minor |
| `breaking` | removal, new requirement, or changed meaning of existing structure | major |

The changeset's floor is the **max** of its per-change floors: one breaking
change makes the whole supersession major, however many annotations ride
along.

**The asymmetric rule:** `declared >= classified`, or the supersession is
rejected. Understating is refused because it is how a breaking change would
sneak past reprocessing as a "patch". Overstating is allowed — an author who
does not trust a change may escalate — and the **declared** severity is the
effective one, so escalation only ever buys more revalidation, never less.
Both severities are recorded on the event (claim vs evidence).

## Reprocessing requirements

Effective severity mandates downstream work, surfaced as a durable
**Requirement** (#128 §16, §18):

| severity | action | downstream meaning |
|---|---|---|
| patch | *(none)* | nothing consumer-visible changed; no requirement emitted |
| minor | `revalidate` | existing outputs presumed valid; the presumption must be confirmed by whatever workflow owns them (#128 §19) |
| major | `reprocess` | existing dependent outputs presumed invalid; dependent work must be redone under the new head. **Staleness source.** |

Requirements are the queue: `PendingRequirements()` surfaces them oldest
first; `ResolveRequirement(id, actor, evidence)` discharges one. A
resolution requires evidence (a non-empty note) and is itself a ledger
event, so the discharge is as inspectable as the trigger.

### The captain-authority rule

VISION.md:21 — "An agent may speak; only the captain decides what happens
next." Records carry **acted-on marks** (`MarkActedOn`); a mark by actor
`captain` is the authority signal. Superseding a captain-acted-on record is
**never silent**, whatever the severity:

- a patch — which normally emits nothing — upgrades to a captain-visible
  `notice` requirement (visibility mandated, work not);
- minor/major requirements become captain-visible;
- a captain-visible requirement may **only be resolved by the captain**.
  Anyone else discharging it would be exactly the silent rewrite the rule
  exists to prevent.

## The staleness seam (task-4cfpv.14)

This package deliberately does **not** own the dependency graph.
`internal/staleness` ([`staleness-model.md`](staleness-model.md), its
"Supersession seam" section) derives which dependents went stale from
version moves; this package decides *whether* a supersession is
staleness-inducing. The seam, from both sides:

- A requirement with `StalenessSource: true` (exactly the `reprocess`
  action, i.e. major severity) is the signal. The wiring layer answers it
  with `staleness.Bump(supersededID, newVersion, "superseded:<severity>")`.
- Minor supersessions do **not** enter the staleness graph: their outputs
  are presumed valid — revalidation is owed, invalidation is not — and
  bumping the graph would erase that distinction. Patch supersessions touch
  nothing.

One requirement per supersession; many stale dependents per requirement —
the fan-out is the staleness engine's job.

## Observability

`Explain(recordID)` answers, from the ledger alone: how the record came to
exist (chain root, or the supersession that created it), why it was
superseded (changeset, reason, declared vs classified severity, actor), what
that triggered (the requirement and its live resolution state), and who
acted on it. `Render()` is the human-readable trace. The event log itself
round-trips through JSON (one object per event) and `Replay` rebuilds the
full state, failing loudly on corruption.

## What this package deliberately is not

- **Not a store**: in-memory fold; persistence and wiring into beads is a
  later unit. The JSON event round-trip is the persistence contract.
- **Not the dependency graph**: see the seam above.
- **Not a migration executor**: *what work* revalidation or reprocessing
  concretely is (rerun, review, escalate, queue) is workflow-defined per
  #128 §16/§21 — this package mandates and tracks the obligation, it does
  not perform it.
- **Not Gas City's drift detector**: `gc formula version-check` remains a
  raw signal; this policy is what consumes such signals on parlay's side.

## Chosen defaults where #128 defers (flagged per the task contract)

1. **Linear chains** — superseding a non-head is rejected rather than
   branching (see "Record chains").
2. **Closed change-class vocabulary** of four classes — an open vocabulary
   would force the engine to guess floors for unknown classes.
3. **Asymmetric bump enforcement** — reject understatement, allow
   escalation, declared severity is effective.
4. **Only major supersessions are staleness-inducing** — minor is owed
   revalidation without invalidation (see the seam).
5. **Captain marks key on the literal actor `captain`** — matching the
   single-captain model of VISION.md ("one person who owns the machine and
   the fleet").
