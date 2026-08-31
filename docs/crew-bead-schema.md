# Crew-bead schema

Status-lift **unit 2** (epic `task-4cfpv.12`; plan: scope-status-lift report §5).
This document is the normative schema for the beads-store representation of
parlay crew status adopted in [status-lift-topology.md](status-lift-topology.md):
what a crew bead looks like, how parlay's status verbs map onto beads statuses,
the metadata key vocabulary, and the three-vocabulary crosswalk required by the
report's §4.2. The machine-readable half is the constant table in
`tools/cli/internal/parlaybeads/schema.go`, which mirrors gascity's
`internal/session/info_codec.go` in shape; this doc and that file must move
together.

**Nothing consumes this schema yet.** Units 0–2 deliver decision, client, and
schema only; the writer (unit 3) and reconciler (unit 4) adopt it later. Until
then today's status-file behavior is byte-identical and this schema is
reversible by editing.

## The bead

One crew bead per parlay agent.

| field | value | why |
|---|---|---|
| issue type | `agent` | crew beads are not tasks; a typed query (`type=agent`) separates them from work items sharing a store |
| label | `parlay-crew` | the enumeration handle: `ListByLabel("parlay-crew")` is how a reconciler finds the crew without knowing ids |
| title | `agent <agent-id>` | human-readable in any bd/beads UI |
| assignee | `<agent-id>` | mirrors `parlay claim` semantics (report §4.1: claim → assignee) |
| status | projection of the last status verb (table below) | store-native queries (`status=blocked`) work without decoding metadata |
| close reason | terminal verb that closed it (`done` / `failed`) | beads has no `failed` status; the distinction survives in CloseReason and `status_verb` |

The bead **id** is store-assigned (`crew-1`, `crew-2`, …). The agent id is
carried by assignee and metadata, never parsed out of the bead id.

## Status mapping (normative)

parlay's writer vocabulary is exactly the 7 verbs of
`tools/cli/internal/commands/status_verb.go` (`statusVerbs`). Beads statuses
are `open`, `in_progress`, `blocked`, `deferred`, `closed`.

| parlay verb | beads status | close reason | terminal |
|---|---|---|---|
| `working` | `in_progress` | — | no |
| `needs-decision` | `blocked` | — | no |
| `blocked` | `blocked` | — | no |
| `paused` | `deferred` | — | no |
| `resolved` | `in_progress` | — | no |
| `done` | `closed` | `done` | yes |
| `failed` | `closed` | `failed` | yes |

Rules the table can't show:

1. **The verb is data; the beads status is a projection.** Every status write
   stores the verb verbatim in `status_verb` and *derives* the beads status
   from this table. Readers that need parlay semantics read the metadata;
   the beads status exists so store-native tooling can query without a
   decoder ring. The mapping is many-to-one (`needs-decision` and `blocked`
   both project to `blocked`) and is not required to be invertible.
2. **`captain-held` is reader-plane vocabulary and is never stored.** It is
   accepted by the crew-state fold (`crew_state.go` `knownStatusVerbs`) and
   defined by firstmate's classifier, but no parlay verb emits it (report
   §4.3.2). It has a row in the crosswalk below and none here.
3. **Terminal verbs close the bead** via `CloseBead` with the verb as the
   close reason. A closed crew bead is what `parlaybeads.AffirmativelyClosed`
   affirms; per the fail-open contract a lookup failure never counts.

## Metadata key vocabulary (normative)

Flat string keys, per-key atomically merged (`MergeMetadata`). Bare names in
gascity `info_codec.go` style.

| key | value | written by |
|---|---|---|
| `agent_id` | the parlay agent id | once, at bead creation |
| `status_verb` | last status verb, verbatim (7-verb vocabulary above) | every status write |
| `status_key` | the `[key=<slug>]` slug of the last write; `""` when the write had none | every status write |
| `status_note` | the free-text note, verbatim | every status write |
| `status_at` | RFC3339 timestamp of the last status write | every status write |
| `decision.<slug>` | `open` \| `resolved` | keyed-decision transitions (below) |
| `gc_session` | the spawn seam's gc session bead id | every status write, when `identity.md` carries the `gc_session` stamp (gc-spawned agents) |

`gc_session` is the **attachment pointer** required by report §6.1 point 4:
the spawn seam owns the agent record (the gc session bead `gc-spawn` mints);
the crew bead is status *attached to* that record, never a second agent
record. Unit 3's writer copies the stamp from `identity.md` frontmatter; an
agent that was not gc-spawned simply has no such key.

Unknown keys are tolerated on read (the client's lenient metadata decode) and
never written: growing this vocabulary is a schema change that lands in this
doc and `schema.go` together, not ad hoc at a call site.

## The status line is a projection

Thirty-odd firstmate scripts parse the exact byte shape
`"<verb> [key=<slug>]: <note>"` (report §4.2). Under this schema that line is
**rendered** from `status_verb`/`status_key`/`status_note` by
`schema.go`'s `RenderStatusLine`, which must stay byte-identical to
`status_verb.go`'s `buildStatusLine` (a test pins the shape). The stored
truth is the typed metadata; the line is a view for consumers that speak the
old grammar.

## Keyed decisions

The `[key=<slug>]` open/close fold has no beads-native primitive (report
§4.3.1). The report offers two representations: a child bead per decision
with a `blocks` dependency, or metadata with per-key compare-and-set. **This
schema adopts the metadata representation**: a `needs-decision [key=X]` or
`blocked [key=X]` write merges `decision.X=open` (both openers, matching
firstmate's fold, which classifies a keyed `blocked` as an open decision the
same as a keyed `needs-decision` — report §4.3); a `resolved [key=X]` write
merges `decision.X=resolved`. Rationale:

- `MergeMetadata` is per-key atomic in the beads API the client already
  wraps; the child-bead route needs dependency APIs `parlaybeads.Client`
  deliberately does not expose.
- The fold ("which keys are open right now?") becomes a map scan over one
  bead's `decision.*` keys instead of a cross-bead dependency query — the
  same locality today's single status file has.
- Reversible: if unit 3+ finds decisions need lifecycle of their own
  (assignees, comments), promotion to child beads is additive; the
  `decision.*` keys read as history, not as a conflicting source of truth.

Slug grammar is inherited unchanged from `statusLineRe`:
`[A-Za-z0-9._-]+`.

## Three-vocabulary crosswalk (informative)

Required by report §4.2: parlay's verbs, Gas City's lifecycle
(`BaseState` × `DesiredState`, `lifecycle_projection.go`), and issue #128 §5's
proposed vocabulary (`active, finished, continue, parked, blocked, queued`)
are three vocabularies, none a superset. This crosswalk is owned here so no
future unit re-derives it differently. Only the "beads status" column above is
normative; these correspondences are nearest-fit, many-to-one, and lossy in
both directions.

| parlay verb | Gas City nearest | #128 §5 nearest | lossage |
|---|---|---|---|
| `working` | `active` | `active` | clean |
| `needs-decision` | `active` + `desired-blocked` | `blocked` | GC has no decision primitive; the *reason* lives only in parlay's `decision.*` keys |
| `blocked` | `active` + `desired-blocked` | `blocked` | GC's `BaseState` has no blocked value — blockedness is controller intent (`DesiredState`), not observed state |
| `paused` | `asleep` / `suspended` | `parked` | GC distinguishes how it was paused; parlay does not |
| `resolved` | `active` | `active` | a routine "decision closed, continuing" beat; GC/#128 see no transition at all |
| `done` | `closed` (via `closing`) | `finished` | clean |
| `failed` | `stopped` / `quarantined` | `blocked` (with error bead) | GC and #128 both model failure as a state needing intervention; parlay models it as terminal |
| `captain-held` (reader-only) | `suspended` | `parked` | firstmate-domain; never stored (rule 2 above) |

Unmapped in the other direction, recorded so nobody "discovers" them later:

- **#128 `continue`** (Ralph-loop re-entry, §5.2) has a Gas City counterpart
  (the `ralph` control kind, `internal/beadmeta/values.go`) and **no parlay
  counterpart at all**. If parlay grows one it enters the verb vocabulary
  through this doc.
- **#128 `queued`** (ready, no capacity) — parlay has no admission control;
  nothing to map.
- **Gas City's creation/teardown states** (`creating`, `start-pending`,
  `failed-create`, `draining`, `drained`, `archived`, `orphaned`,
  `closing`) describe process lifecycle parlay's *enrollment tri-state*
  observes, not anything a status verb expresses. The reconciler (unit 4)
  composes bead status × enrollment exactly so these never need verb
  equivalents.
