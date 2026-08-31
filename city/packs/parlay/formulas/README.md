# parlay formulas — issue-128 semantics mapped to Gas City constructs

Real formulas land here as canonical `<name>.toml` files (Formula Spec v2;
`docs/reference/specs/formula-spec-v2.md` at the pinned Gas City ref
`7c817e064`, readable in `~/code/gascity`). This file stubs the mapping from
the workflow semantics of GitHub issue #128 ("Parlay — Architecture Discussion
and Working Model", trillium/parlay) to the Gas City constructs that express
them — **pointers, not new semantics**. Where a semantic has no Gas City
construct, the owning parlay task is named instead; do not invent a formula
shape for it.

The kill-switch verification that formulas can express these semantics is
task-4cfpv.1 (closed, YES); the end-to-end proof is the §63 mechanical SMS
workflow spike, task-4cfpv.2 (closed — 13 beads, 12 typed dependency edges,
ran to completion under `GC_BEADS=file`).

## The mapping

| issue-128 semantic (§) | Gas City construct | Pointer |
|---|---|---|
| Workflows are beads (§10); steps are beads (§11) | Formula → workflow root bead + step beads, flat graph | formula-spec-v2 §0.2; spike task-4cfpv.2 |
| Workflow templates / reuse (§12, §27) | Formula TOML files in this directory; `expand` / `expand_vars` inline an expansion formula | formula-spec-v2 §1.3, §1.7 |
| Conditional branching (§8, §89–92) | Step `condition` — compile-time include/exclude on `{{var}}` | formula-spec-v2 §1.5; **caveat:** condition-excluded steps silently orphan their dependents (brain-8zj8f) |
| Dependency edges (§23–24) | Step `needs` / `depends_on` (merged at cook) | formula-spec-v2 §1.3; **caveat:** unknown step keys are silently ignored — a typo drops the edge with no diagnostic |
| Mechanical workflows, no inference (§62–63) | Control beads (check, retry, drain, fanout, finalize) run by the orchestrator's control dispatcher; agents run only plain work beads | formula-spec-v2 §0.2, §3; spike task-4cfpv.2 |
| Refinement / retry loops (§93) | Step `check` (run/verify loop) and `retry` (transient budget) — graph-only keys requiring the explicit v2 declaration | formula-spec-v2 §3.1–3.2, §5 |
| Task triggering on schedule (§40) | Orders, not formulas — `../orders/` | pack-spec §1.3.5; `engdocs/architecture/orders.md` |
| Workflow supersession & SemVer severity (§13–18) | **No Gas City construct.** `gc formula version-check` emits a drift *signal* only; the migrate/supersede/severity *policy* is parlay's | task-4cfpv.13; plane-boundary register row 4 |
| Staleness propagation (§21–22) | **No Gas City construct** — parlay-owned policy | task-4cfpv.14 |
| Deterministic routing + confidence (§34–37) | **parlay-owned policy**; Gas City's address resolution refuses ambiguity but decides nothing | task-4cfpv.17; plane-boundary §2.7 |
| Source contracts / enrollment (§28–32, §70–77) | **parlay-owned** | task-4cfpv.15 |

## Authoring rules for formulas dropped in here

1. Canonical file name is `<name>.toml` (`<name>.formula.toml` is deprecated);
   within a layer the canonical name wins (pack-spec §2.9).
2. This directory is one formula *layer*: a consuming city or rig can shadow a
   formula here by name from a higher-priority layer. Name formulas
   distinctively (`parlay-<purpose>`) to keep collisions deliberate.
3. Steps carry work as `description` text for agents; Gas City executes the
   *graph* and the control beads. A step needing prose beyond a line should use
   `description_file = "../assets/<path>"` so the prompt lives under `assets/`
   and stays layer-shadowable (pack-spec §1.3.2).
4. Nothing here may assume a bead backend until Q4 (task-4cfpv.20) is ruled.
   The spike ran under `GC_BEADS=file`, but that substrate is ruled out as
   durable storage (epic amendment §5) — treat backend behavior as undecided.
