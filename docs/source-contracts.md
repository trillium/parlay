# Source enrollment contracts for human input surfaces

Deliverable of task-4cfpv.15 (epic child of issue #128 §28–§33, §71–§79,
§104–§109). Engine (next unit): `tools/cli/internal/sourcecontract` — pure,
deterministic, no I/O, no clock, no inference, following the
`internal/routing` / `internal/staleness` / `internal/supersession` pattern.
This document is the contract-schema and enrollment design those units
implement.

**Scope disambiguation first:** a *source* here is a human input surface —
Apple Notes, voice dictation, a terminal, a web panel, a phone widget
(#128 §28). It is not an agent. Agent enrollment (`register-agent`,
`declare-channel`, `poll`, `parlay listen`) is a different mechanism with a
different lifecycle and stays untouched. It is also not a store: "the source
is not itself a store; the input eventually becomes or contributes to a bead
in a store" (#128 §75, §104). And it is representation-plane, core-parlay
work — independent of the Gas City execution-plane seams
(`docs/gascity-plane-boundary.md`).

## The problem

VISION.md exists so that one human can direct a fleet from a phone without a
keyboard. That requires "any input can potentially participate in the
control plane" (#128 §33): the captain dictates into Apple Notes, speaks
into a voice memo, taps a widget — and the input reaches parlay carrying
enough metadata to be routed, attributed, and reproduced later.

Today every surface that gets input into parlay is special-cased in code:
its identity is a hard-coded string, its event name is a hard-coded
allowlist entry, its metadata is whatever the producing code happens to
attach. #128 §29 names the fix: "When a source is enrolled into Parlay, its
metadata contract is defined. The source's metadata does not simply get
invented ad hoc every time an input arrives." A new surface should enroll
by **declaring a contract**, not by patching switch statements — while the
security boundary (an unauthenticated chat API guarded by what handlers DO)
must not gain a single new hole from the existence of enrollment.

## The model in one paragraph

A **source contract** is a versioned, immutable declaration of everything
parlay knows about one input surface: its identity, the origin metadata it
stamps on every input, its delivery semantics, its interaction capabilities,
and its trust posture (#128 §30, §76). Enrollment is the act of adding that
declaration to the checked-in contract registry — a repository change that
lands through the same protected-main PR flow as code, never an API call, so
enrollment authority is exactly repo authority: the captain's. A registry
engine validates every declaration against closed vocabularies and refuses
anything that would touch the guarded surface; enforcement points (the
events-ingress allowlist first) then *derive* their per-producer allowlists
from enrolled contracts instead of hard-coding names, with the dangerous
name sets refused structurally on both sides. Contract versions follow the
supersession model (`docs/supersession.md`, kind `source-contract` — #128
§31, §77): superseded, never mutated, so every bead keeps "its origin, and
the exact contract it adhered to" (#128 §30, §78).

## Today's special-cased surfaces (the inventory this replaces)

Every current surface, and the mechanism that lets it in. (Routes below are
the existing ingress seams; trust postures are defined in "What a surface
declares".)

| Surface | Gets in via | Special-cased how | Posture it maps to |
|---|---|---|---|
| Web panel composer | `POST /api/chat/send`, `PUT /draft` | the *unmarked default*: no `from`, no `source` — absence is the captain's identity (`packages/server/src/router-messages.ts`) | `control` |
| Voice dictation (Talon → composer) | `POST /api/chat/eval` / `eval-push` | hard-coded phrase manifest (`packages/eval-engine/default_commands.json`), device-keyed stream map | `control` |
| Cursorless plugin RPC (Talon) | `POST /api/chat/plugin/cursorless/rpc` | plugin id `"cursorless"` hard-coded in the manifest list, the path switch, and the guard entry | `control` |
| PAI **tool** tailer | `POST /api/chat/events` | `"tool_event"` sole member of the ingress allowlist (`events_ingress.go`) *and* of the Gas City bus dual-write list (`events.go` `busEmitEvents`); `tool_name === "Monitor"` special case for session enrollment | `observability` |
| PAI **hook** tailer | `POST /api/chat/message` | free-form `source` (verbatim from an out-of-repo JSONL, ≤60 chars, `"hook"` fallback); `source: "turn"` drives a bespoke length cap | `content` |
| Hooks / system lines | `POST /api/chat/system` | free-form `source`; TS stores it top-level, Go stores it in `meta` — an undocumented divergence | `content` |
| `parlay` CLI verbs | `/send`, `/reply`, `/message`, `/alert` | identity is an env var copied into a body field; no session auth | `content` / `control` |
| TTS playback lifecycle | `POST /api/chat/tts-event` | pseudo-role `"tts_event"` outside the `ChatMessage.role` union; hard-coded event type strings | `observability` |

Two findings shape the design:

1. **Input surfaces do not enroll at all today.** Agents enroll
   (`register-agent`; `declare-channel` for session→channel mapping; page
   inputs via `__paRegisterInput` — all out of scope here). A *surface*
   gets in by one of three unauthenticated mechanisms: a hard-coded event
   name in an allowlist, membership of a guard path set, or free-form body
   fields. There is no per-source identity, no capability declaration, no
   metadata contract.
2. **`source` is exactly the ad-hoc metadata #128 §29 warns about.** It is
   validated nowhere, enumerated nowhere, and populated verbatim from
   out-of-repo producers; `docs/api-contract.md` documents it as "only the
   label the panel prints". The declared origin-metadata contract below is
   its replacement path.

## What a surface declares

The contract schema. One declaration file per surface under
`contracts/sources/<name>.json` (canonical location; see "Enrollment
mechanics").

### 1. Identity (#128 §28, §71)

- `name` — stable slug, the chain identity. Once enrolled, never reused for
  a different surface; supersession fixes `Name` and `Kind` for a chain's
  life.
- `title`, `description` — human-facing.

Identity is deliberately separate from capability: "The source's
identity/origin remains one thing. Its support/capability declaration is
another." (#128 §71).

### 2. Origin metadata fields (#128 §28–§30)

`origin.fields[]` — the metadata this source supplies with every input:

```json
{ "name": "note_id", "type": "id", "required": true,
  "description": "Apple Notes note identifier" }
```

Types are a closed set: `string`, `int`, `bool`, `timestamp`, `id`. The
origin travels with the bead forever — later edits through other surfaces
land in edit history and never replace it (#128 §32).

In v1 these declarations are **descriptive, versioned truth**, not yet a
runtime validator: the fix #128 §29 asks for is that the metadata contract
*exists and is versioned* instead of being invented ad hoc per call site.
Runtime shape enforcement belongs to whatever route ingests the input and
arrives with bead-store integration, not before. (Chosen default — #128
does not say where enforcement runs.)

### 3. Delivery semantics

`delivery` — how inputs travel from the surface into parlay:

- `mode`: `push` (the surface posts in) or `pull` (parlay tails/polls it —
  the tailers, an Apple Notes folder watcher).
- `route`: which **existing** ingress route carries the input, from a
  closed route table (see "The security story"). A contract cannot name a
  route that does not already exist.
- `ordering`: `ordered` | `unordered`; `guarantee`: `at-least-once` |
  `at-most-once`.

(Chosen vocabulary — #128 names delivery nowhere; the task brief names it
as contract content, and the values are the ones today's producers already
exhibit.)

### 4. Capabilities (#128 §72–§74)

`capabilities[]` — structured support declarations, not booleans: "Supports
SMS may be too coarse … support/capability relationships could be
structured rather than being a simple boolean. The exact representation is
TBD." (#128 §72). Chosen representation, since #128 leaves it open: a flat
list of interaction verbs from a closed initial vocabulary —

`originate` (create new input), `view`, `compose`, `send`, `select`,
`confirm`

— the verbs #128 §72 itself enumerates for the SMS example, plus
`originate` for pure input sources. Apple Notes declares `originate` and
nothing else; a terminal might add `view` and `select`; the web panel
declares all of them. Workflows stay UI-independent (#128 §73): a workflow
names an abstract state, and the capability declaration tells parlay which
enrolled surfaces can represent it (#128 §105). Growing the vocabulary is
an engine change (new validated token), not a free-form field.

### 5. Trust posture

`trust` — one of a closed set of postures, ordered by what the surface may
aim at the system. #128 is silent on trust; the classes are derived from
the two refusal reasons the events-ingress seam already enforces
(`docs/agent-notes/out-of-process-producers-reach-the.md`) and VISION.md's
security boundary ("a cross-origin page, a malicious message body, or a
misbehaving agent cannot steer the crew"):

- `observability` — may emit **non-persisted observability frames** (the
  `tool_event` class) through the events ingress. May not originate
  persisted content, may not aim anything at the panel or captain.
- `content` — may **originate persisted input** (messages/beads) through a
  persisting route, stamped with its origin metadata. May not emit raw SSE
  frames.
- `control` — a captain-facing interactive surface (panel, voice, CLI)
  that participates in interaction workflows (#128 §33, §108). May declare
  the full capability vocabulary.

Posture bounds declaration: validation refuses an `observability` contract
declaring interactive capabilities, a `content` contract declaring `emits`,
and so on. Every posture is refused the forbidden event-name sets — no
declaration, at any trust level, can name a panel-aiming or server-owned
event (see "The security story").

### 6. Emits (observability posture only)

`emits[]` — the SSE event names this producer is allowed to put through
`POST /api/chat/events`. One name per real producer stays the law; the
contract is now the durable record of which producer owns which name, next
to its identity and version instead of next to a code comment.

### 7. Version (#128 §31, §77)

`version` — strict numeric `MAJOR.MINOR.PATCH`, same parser discipline as
`internal/supersession` (no prerelease, no `v`). A contract change follows
the supersession model with kind `source-contract`: superseded never
mutated, bump validated against the changeset's severity floor, and a major
bump is a staleness source for source-originated beads (#128 §77: "existing
source-originated beads may or may not become stale"). Wiring contract
records into the supersession ledger is a follow-up unit; the schema
carries the version from day one so the chain is well-formed when it lands.

## Enrollment mechanics

**Enrollment is a repository change, not an API call.** To enroll a
surface: add `contracts/sources/<name>.json`, pass validation, land it on
main through the protected-PR flow. To supersede: replace the file's
content with the next version (git history preserves every prior version —
records are never destroyed, #128 §15, §79; the checked-in head plus git
history is today's representation of the contract-version chain until the
supersession-ledger integration lands).

Why this is the right v1 mechanism and not a runtime enrollment endpoint:

1. **It is structurally incapable of being a hole.** The chat API has no
   authentication; any runtime "enroll" route would be a new mutating,
   unauthenticated write path into the exact seam the guard exists to
   protect. A checked-in file has no route, no handler, no origin to
   guard. Enrollment authority collapses onto repo authority — branch
   protection, required checks, `enforce_admins` — which is already
   exactly the captain's authority and nobody else's (VISION.md: no user
   role beyond captain and crew).
2. **It is deterministic** (#128 §2): the enrolled set is a pure function
   of the repo tree. Two builds of the same commit agree about every
   contract.
3. **It matches the cadence.** Surfaces enroll rarely — the churn is in
   inputs, not in surfaces.

The contract-as-bead destination (#128 §76: "the source contract itself can
be a bead") is not abandoned: the declaration carries stable identity and
version precisely so that when contract beads land, each checked-in head
becomes a bead record with its chain intact. The file is today's
representation of that bead. (Chosen default — #128 says "could itself be
represented as a contract bead"; a bead store for contracts does not exist
yet, and inventing one here would couple this unit to storage design.)

### The registry engine

`tools/cli/internal/sourcecontract` — pure house-style engine:

- `Parse(raw []byte) (Contract, error)` — one declaration, strict: unknown
  fields, unknown vocabulary tokens, and duplicate names are errors, loudly
  (parlay does not guess).
- `Validate(c Contract) error` — closed-vocabulary checks, posture/
  declaration consistency, forbidden-name refusal.
- `Load(raws map[string][]byte) (Registry, error)` — the full enrolled
  set: per-contract validation plus cross-contract invariants (unique
  `name`; no event name claimed by two producers — one name per real
  producer is registry-enforced, not comment-enforced).
- `Registry` answers: `ByName`, `IngressEventNames()` (the derived
  allowlist), `Capable(interaction)` (#128 §105's "which surfaces can
  represent this state").

No I/O: callers hand it bytes. The CLI test suite loads the canonical
`contracts/sources/` tree through it, so an invalid canonical contract is a
red build, not a runtime surprise.

## The security story

The prime rule: **enrollment must not widen the guarded surface, and a
contract must not be able to ask it to.** Concretely, against each element
of the boundary (CLAUDE.md "The security boundary"):

**`GUARDED_CHAT_PATHS` / Go `GuardedPaths` — unchanged, unreferenced by
data.** A contract's `delivery.route` must name a route from a closed table
of ingress routes that already exist (v1: `POST /api/chat/events`,
`POST /api/chat/message`, the plugin-RPC prefix). A contract naming any
other route — guarded or not, real or invented — is refused at validation.
No code path derives guard membership from contract data in either
direction: a surface that someday needs a genuinely new route needs a code
change that adds the route AND its guard entry on both sides, exactly as
today, and only then can a contract name it. Route-table growth is an
engine change reviewed like a guard change.

**The events allowlist — derived, never widened.** The enforcement point
(`packages/go-server/internal/handlers/events_ingress.go`) stops
hard-coding `tool_event` and derives its allowlist from the enrolled
`observability` contracts. Three locks keep derivation from becoming
widening:

1. **The forbidden sets stay hard-coded in Go, in the ingress,** exactly
   as documented in
   `docs/agent-notes/out-of-process-producers-reach-the.md`: the
   panel-aiming names (`navigate`, `reload`, `device_cmd`, `input_action`,
   `draft`) and the server-owned persisted-state names (`connected`,
   `history`, `agents`, `agent_register`, `presence_map`, `message`,
   `message_received`, `commands`, `command_update` — `events_ingress.go`
   remains the contract owner of that roster). If a derived name ever
   intersects either set, the server refuses to start — fail closed at
   boot, not 200 at runtime.
2. **The engine refuses the same sets at validation**, so the bad contract
   cannot land on main in the first place (CI runs the registry load).
   Defense in depth: the two checks share a vocabulary but not a code
   path, so neither's bug unlocks the other.
3. **Unknown names still 400** at the ingress; a missing or unparseable
   embedded registry yields an **empty** allowlist, never a pass-through.
   Fail closed is the default posture everywhere (VISION.md: "refusal is
   the default when the check is inconclusive").

Net effect on day one: the derived allowlist is exactly `{"tool_event"}` —
byte-identical ingress behavior, with the producer's record moved from a
code comment into a versioned contract. Only `ingressEvents` is derived:
the companion `busEmitEvents` set (the Gas City bus dual-write filter in
`events.go`) mixes server-produced names with ingress names and stays
hard-coded; deriving its ingress-name members is a follow-up, not part of
the proof.

**`JSON_EXEMPT_PATHS` — untouchable.** Contracts have no field that can
reference it; the list stays a closed three-member set. A contract cannot
declare a content-type exemption, an origin-policy change, or anything else
about how the guard treats its route. Trust posture selects *which existing
route* carries input; it never modulates the guard's treatment of that
route.

**Origin metadata vs the identifier surface.** Contracts declare metadata
the *surface supplies*; they cannot declare access to identifiers the
server holds (device ids, agent ids). Nothing in the schema names another
surface's data. The live-command-registry rule (no free-form text in
registries) is inherited: the contract registry stores declarations —
names, closed-vocabulary tokens, field specs — never input content, argv,
or paths.

## How the contract replaces special-casing

| Today | With contracts |
|---|---|
| `tool_event` hard-coded in `events_ingress.go` with a comment naming the producer | derived from the enrolled `tool-tailer` contract; the comment's content becomes versioned data |
| a producer's identity lives in the string it happens to send | `name` is chain identity; the registry rejects two claimants to one event name |
| metadata shape is whatever the producing code attaches | `origin.fields` is declared, versioned truth per #128 §29 |
| "can Apple Notes do X?" answered by reading code | `Capable(interaction)` answered by the registry per #128 §72–§74, §105 |
| adding a surface = patching allowlists + switch statements | adding a surface = one declaration file + the minimal producer code, with validation refusing anything boundary-touching |

The migration is incremental and each step keeps behavior byte-identical
unless its PR says otherwise: engine first (leaf package, no callers), then
one real surface — the tool tailer, the simplest existing special case —
enrolled end-to-end as the proof, then the remaining surfaces as
follow-ups.

### Cross-module plumbing (chosen default)

`packages/go-server` and `tools/cli` are separate zero-dependency Go
modules, and `internal/` does not cross module boundaries. The established
repo pattern for sharing checked-in data across that boundary is
`cityscaffold`'s: an embedded mirror plus a sync test that fails when
mirror and canonical diverge. PR 3 follows it — canonical
`contracts/sources/` at the repo root (validated by the `tools/cli`
engine), a `go:embed` mirror inside `packages/go-server` with a sync test,
and a minimal loader there that reads only what enforcement needs (names,
posture, emits). The TS side (`packages/server`) keeps its producers
unchanged; a package test pins each producer's hard-coded name to the
canonical contract so drift is a red test, not a silent fork.

## Chosen defaults where #128 is silent (summary)

1. **Capability representation** (#128 §72 "exact representation is TBD"):
   flat closed-vocabulary interaction list; structure can deepen later via
   schema versioning.
2. **Trust postures**: not in #128 at all; derived from the ingress seam's
   two refusal reasons plus VISION.md's boundary sentence.
3. **Delivery vocabulary**: not in #128; values chosen from what today's
   producers already exhibit.
4. **Enrollment as repo change**: #128 §29 defines *that* enrollment
   defines the metadata contract, not *how* enrollment happens; chosen for
   the security and determinism reasons above.
5. **Contract storage as checked-in files** pending contract beads
   (#128 §76 "could"): stable identity+version keep the future migration
   chain-preserving.
6. **Descriptive (not runtime-enforced) metadata fields in v1**: #128 does
   not place enforcement; placing it at bead-store ingestion later avoids
   teaching the ingress to inspect frame bodies it deliberately passes
   through untouched.
7. **Where #128 §30 says a contract "can define a workflow"**: out of v1
   scope; the schema reserves no field for it, and adding one is an
   additive (minor) schema change when workflows-as-beads integration
   arrives.

## Unit plan

1. **This document** — the schema and the security story (this PR).
2. **`tools/cli/internal/sourcecontract`** — types, `Parse`/`Validate`/
   `Load`, `Registry`, exhaustive tests including the forbidden-set
   refusals and cross-contract invariants. Leaf package, no callers yet.
3. **Enroll the tool tailer** — canonical
   `contracts/sources/tool-tailer.json`; engine test loads the canonical
   tree; go-server embedded mirror + sync test + derived allowlist with
   hard-coded forbidden sets and fail-closed boot check; TS-side name pin.
   Ingress behavior byte-identical (`{"tool_event"}`, unknown names 400).
4. **Follow-ups** — remaining surfaces (hook tailer as a `content`
   producer, cursorless plugin as `control`), supersession-ledger
   integration for contract versions, capability-driven representation
   (#128 §105) when interaction workflows land.
