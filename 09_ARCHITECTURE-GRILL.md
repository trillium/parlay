# 09 — Architecture grill, Round 5 (agent — recording the captain's decision)

**Status recap.** Round 4's five open questions (Q2b, Q2c, Q2d, Q6b) plus
Q16 were answered by the captain as a **directive** rather than through an
even-numbered reply file: this repo adopts Gas City as its execution plane.
`08` was not created — this round is agent-authored documentation of a
settled captain decision, so it lands in the next odd file (agent = odd per
the `00` protocol; IDs are never reused, so the number is skipped, not
reassigned). The authoritative record of what is decided lives in the
consensus register in `00_ARCHITECTURE-GRILL-META.md`; this file carries the
reasoning, the verified gascity facts, and the gates that stay open.

Every claim below about gascity was checked against the read-only clone at
`~/code/gascity` (module `github.com/gastownhall/gascity`, LICENSE = MIT,
Copyright 2025, Steve Yegge; tagged releases exist — `git tag` reaches
`v1.4.1` — so the adoption can pin a release rather than branch HEAD).
Where the brief's claims and the repo disagree, this file says so plainly.

---

## The decision: the plane split

Parlay adopts **Gas City** as its **execution plane**. Parlay retains the
**process and representation plane**.

- **Gas City supplies:** sessions and their runtime providers, liveness and
  process control, supervision (health patrol), dispatch (sling), the
  append-only event bus, the bead store, orders, mail, the controller, the
  config/pack system, and the typed HTTP/SSE API control plane.
- **Parlay owns:** routing with confidence and progressive hardening,
  staleness propagation, supersession and SemVer-classified migration
  severity, source enrollment contracts for human surfaces (Apple Notes,
  voice, dictation), granular interface capability declaration for
  representation, transient interaction state as records, and the
  human/voice relay itself.

This is a documentation-round change only. No code has moved; the ticket
plan below is re-cut to say what a follow-up builds against.

---

## Q16 (REOPENED) · Gascity leverage — corrected premise, new verdict — RESOLVED

### Why the original verdict is reopened

Round 4's survey answered the standing directive
("actively find scenarios where gascity can take over hard parts of parlay")
with an **anti-recommendation against importing gascity as a Go dependency**,
grounded in the `internal/` wall. That measurement surface was wrong for the
question.

The premise that does not hold: *"leverage gascity" must mean Go-import
availability.* Q16's original survey ranked the options by importability and
found essentially the whole module under `internal/`. That is verified fact —
`~/code/gascity/internal/` has **114 packages**, and `pkg/` contains exactly
one exported package, `eventexport`. But Go-import availability is **not**
Gas City's designed extension surface, and measuring leverage that way made
the `internal/` wall look like a ceiling when it is a private-implementation
detail. Gas City's designed extension surface is:

- `city.toml` + `pack.toml` configuration and the pack system
  (`docs/reference/specs/pack-spec.md`),
- pack-defined formulas, orders, skills, commands, providers, and patches,
- runtime providers (`internal/runtime`),
- a typed HTTP + SSE API with generated clients, the OpenAPI 3.1 spec, and a
  registered event-payload type system (`engdocs/architecture/api-control-plane.md`).

On that surface, the correct answer to "where do we maintain less while
gascity trucks along" is: **run gascity as the execution plane** — exactly the
wholesale option Round 4 recommended *against* ("the one that contradicts
everything else you've decided; recommend no"). The captain has now decided
it is the product direction, and with it the Q16 verdict is reversed.

### What the verification pass confirms

Gascity's stated architecture invariant is *"the object model is the center;
the CLI and the HTTP + SSE API are projections over it"* and *"typed data
end-to-end"* (`engdocs/architecture/api-control-plane.md`, "Two architectural
themes"). That is issue #128's own thesis — mechanical movement over defined
processes, typed wire, no re-implemented semantic layer — already shipping.

Roughly two thirds of #128's concepts already exist in gascity. Each was
verified against `~/code/gascity`:

| #128 concept | Verified in gascity | Evidence (file) |
|---|---|---|
| "Everything is a bead"; bead store | Yes | `engdocs/architecture/beads.md`; `internal/beads/beads.go` |
| Store interface over four backends (BdStore / FileStore / MemStore / exec) | Yes | `engdocs/architecture/beads.md` §Architecture |
| Append-only event bus with cursors | Yes — seq-numbered, `Watch(ctx, afterSeq)` | `engdocs/architecture/event-bus.md` |
| Formulas (process definitions as data) | Yes — `*.formula.toml` layers → `.beads/formulas/` | `engdocs/architecture/formulas.md`; `docs/reference/specs/formula-spec-v2.md` |
| Molecules (process instances as bead trees) | Yes — root `molecule` bead + step beads via `MolCook` | `engdocs/architecture/beads.md` |
| Session runtime providers (tmux / subprocess / exec / k8s) | Yes — plus `acp`, `auto`, `hybrid`, `herdr`, `ssh`, `t3bridge` | `engdocs/architecture/session.md`; `internal/runtime/` |
| Dispatch (sling) | Yes | `engdocs/architecture/dispatch.md` |
| Health Patrol (supervision, liveness, crash tracking, idle detection) | Yes | `engdocs/architecture/health-patrol.md` |
| Controller (config watch + reconciliation tick + order dispatch) | Yes | `engdocs/architecture/controller.md` |
| Orders (trigger-conditioned dispatch) | Yes — cooldown/cron/condition/event/manual | `engdocs/architecture/orders.md` |
| Messaging (mail via beads, nudge via Session) | Yes — mail = `type:"message"` beads; nudge = `runtime.Provider.Nudge()` | `engdocs/architecture/messaging.md` |
| Config system | Yes — `city.toml`, progressive activation, packs | `engdocs/architecture/config.md`; `docs/reference/specs/pack-spec.md` |
| API control plane (typed wire, generated clients) | Yes | `engdocs/architecture/api-control-plane.md` |

What gascity does **not** hold is exactly parlay's differentiated product.
These were confirmed as absent from the gascity architecture docs and specs
surveyed (absence claims are survey-scoped, not exhaustively proven):

- **Supersession and SemVer-classified migration severity** — #128's
  "Workflow versioning and supersession." Gascity packs have version
  constraints and lockfiles, not parlay's supersession-and-migration-severity
  semantics.
- **Staleness propagation along dependency edges** — #128's "Dependencies and
  staleness." Gascity formulas express dependency *edges*; nothing in the
  surveyed docs propagates staleness along them.
- **Source enrollment contracts for human surfaces** (Apple Notes, voice,
  dictation) — #128's "Interface/source support and representation." Gascity
  has no representation-plane contract for enrolling human surfaces.
- **Granular interface capability declaration for representation** — again
  #128's interface/source work; not in the surveyed docs.
- **Deterministic routing with confidence and progressive hardening** —
  #128's "Deterministic routing and progressive routing hardening." Gascity
  routes via sling and `internal/graphroute`, but the confidence/progressive-
  hardening model is not there.
- **Transient interaction state as records** — note: gascity *does* have
  blocked-session waits and pending-interaction state (`internal/session/`;
  `engdocs/architecture/session.md` "Waits and pending interactions"). The
  *machine-readable record* treatment of transient interaction state that
  #128 calls for is not evidenced in the surveyed docs; this line is the
  negotiated boundary, to be pinned down during the kill-switch verification.
- **The human/voice relay itself** — parlay's, full stop.

### #128 §82 does not contradict this decision

#128 §82 ("Parlay and Agent Orchestration") lists *"Gastown mayor-style
orchestration"* alongside First Mate as a frustration. Verified against the
issue: the anti-pattern it names is the **continuously occupied mayor
agent** — *"a single central intelligence to continuously reason over
everything"*, agents that "become a bottleneck" coordinating sub-agents.
That critiques the *mayor agent* pattern, not gascity's mechanical substrate.
A future reader must not read §82 as contradicting this decision: the
**substrate** (controller, dispatch, event bus, health patrol) is mechanical,
event-driven, and decentralization-serving — it is the mayor-abstinence §82
asks for. The **mayor** is not adopted. The distinction is recorded here so
§82 and this verdict are read as consistent.

### New verdict — RESOLVED

Q16's standing directive is **discharged**: gascity is adopted as parlay's
execution plane; parlay retains the process and representation plane. The
original "decline the wholesale option to stay a single binary" conclusion is
superseded — see the open gate below, which records that cost as a captain
decision rather than sweeping it under the verdict.

---

## Open gate (captain decision, NOT settled)

**Adopting gascity ends parlay's single-self-contained-binary property.**
Round 4 said of the wholesale option: *"the cost is a `city.toml`, a
controller dependency, tmux/git/jq/pgrep/lsof on PATH, and a `gc start` —
i.e. parlay stops being a single binary you can hand someone,"* and that was
part of why Q16 recommended against it. That cost is real and **unchanged**.
It is recorded here as an **unresolved captain decision**, deliberately NOT
slid inside the Q16 verdict: the execution-plane adoption proceeds with this
cost stated, and the captain holds the final call on how the shipping/deploy
story absorbs it (bundled city, install-time `gc`, or a runtime prerequisite
documented the way `bd` already is under Q5b). Until that call, the practical
installs keep gascity as the launch entry point per Q10 while the packaged
footprint question stays open.

---

## Kill-switch condition (verify before any code moves)

**If Gas City formulas cannot express #128's workflow-bead semantics, the
split shrinks.** The conditional: if formulas only express linear step
sequences, then gascity is still the execution plane (sessions, liveness,
event bus, dispatch) but parlay **keeps its own process model** rather than
representing workflows as gascity formulas.

What the repo shows now, for the next round to weigh:

- The **v2 formula contract is already not merely linear**: `check`, `retry`,
  `fanout`, `drain`, `on_complete`, and `timeout` are graph-only constructs
  with a `formula_compiler >=2.0.0` declaration
  (`docs/reference/specs/formula-spec-v2.md`, §1 "constructs marked
  **graph-only**"), and step beads are independently routable with per-step
  routing intent resolved at dispatch.
- That says "not only linear step sequences" — it does **not** yet say
  "can express supersession, SemVer-classified migration severity, and
  staleness propagation along dependency edges." Those are the #128 semantics
  the kill-switch is really about, and the spec does not settle them.

**This is the thing to verify before any code moves**: a concrete spike that
expresses one #128 workflow (versioned, with supersession and a dependency
edge) as a v2 formula and confirms the control-dispatch runtime honors it.
The outcome decides how much of parlay's process model lands on gascity
formulas vs. stays parlay-owned. Recorded as a PENDING-VERIFY gate, not a
verdict. In the ticket re-cut below, the tickets whose breadth depends on it
are marked (not guessed at).

---

## Q2b · What is a plugin, concretely? — RESOLVED (plugin = a Gas City Pack)

**Decision: a plugin IS a Gas City Pack, per the Pack Specification 2.0.**
Parlay does not design a bespoke plugin format. Round 4's recommendation —
"a manifest + executable in a plugin dir; the server discovers it at
startup" — is superseded by the pack system, which is exactly that shape,
already shipped, already discovered, already versioned.

The Pack Specification 2.0 (`~/code/gascity/docs/reference/specs/pack-spec.md`)
answers Q2b's three questions directly; summary of the surface, discovery,
and contract it specifies:

- **Surface.** A pack is a directory with a required `pack.toml` manifest
  (`[pack]` table: name, schema = 2, optional version / requires_gc) and
  zero or more well-known definition directories — `agents/`, `assets/`,
  `commands/`, `doctor/`, `formulas/`, `orders/`, `skills/`, `mcp/`,
  `template-fragments/`, `overlay/` — plus inline tables for named sessions,
  services, provider presets, agent patches, globals, and pricing
  (§0.3, §1.1). Unknown fields are rejected, not ignored (§1.2). Capability
  is declared structurally: a `commands/<path>/run.sh` defines a command
  leaf, an `agents/<name>/agent.toml` defines an agent template, a
  `formulas/<name>.toml` defines a workflow method.
- **Discovery.** Packs load through imports (`[imports.<binding>]`, source +
  optional version constraint), resolved to a concrete pack root by the
  loader's `gc pack` / `gc import` surface, then expanded city-level and
  rig-level; the loader is deterministic (bindings sorted, layers ordered),
  rejects dependency cycles, and namespaces contributed definitions by
  binding and rig stamping (§1.2.3, §2.1–§2.5). Collisions: duplicate
  qualified names on the same surface fail loading (§2.5).
- **Contract.** Imported definitions are lower-priority base layers;
  patches (`[[patches.agent]]`) and defaults modify without forking;
  scope filtering (city/rig/omitted) governs where a definition instantiates;
  pack-relative paths resolve against the declaring pack; requirements
  (`[[pack.requires]]`) are validated after expansion, and loading fails on
  violation (§1.2.2, §2.4–§2.8, §2.12). `requires_gc` is preserved as
  metadata but is **not yet enforced** by the loader — authors cannot rely on
  it as a hard gate (§0.2).

Consequence for parlay: a parlay capability plugin is a pack — formula,
command, provider preset, or agent template authored to the pack format and
loaded through `gc`'s discovery. That kills the bespoke manifest-and-verb
design in Round 4's T-15 (see the plan re-cut) and gives parlay versioning,
a registry, deterministic load order, and patch-ability for free.

---

## Q2d · The generic UI-command protocol — RESOLVED (adopt gascity's typed wire)

**Decision: the generic UI-command protocol runs on gascity's typed HTTP +
SSE wire contract, under the CLI-and-API-as-projections invariant.** Round 4
asked who issues a UI command and proposed a typed envelope with
capability-declared subscribers. The design question is answered by taking
the machinery that already ships.

The contract, as specified and verified in
`~/code/gascity/engdocs/architecture/api-control-plane.md`:

- **Object model at the center; CLI and HTTP + SSE API are projections over
  it.** One canonical domain in `internal/`; neither surface re-implements
  validation or invariants (§1). Parlay's relay/panel surface becomes
  another subscriber/projection over the same rule, rather than a parallel
  bespoke routing layer.
- **Typed data end-to-end.** Go structs annotated for Huma generate the
  OpenAPI 3.1 spec; the spec drives generated clients (Go via oapi-codegen
  under `internal/api/genclient/`, TypeScript for the dashboard SPA), and
  spec/reality drift fails CI (`TestOpenAPISpecInSync`,
  `TestGeneratedClientInSync`, layer-2 round-trips) (§2, §5). Every
  wire-visible shape appears in the spec — no hand-constructed JSON for
  domain data, no `map[string]any` in the typed control plane (§3.4–§3.5).
- **Event payload registry.** Every event type has a registered typed wire
  payload: `events.RegisterPayload(constant, sample)` + a sealed `events.Payload`
  interface; a `oneOf` union describes every payload on every SSE stream and
  list endpoint; `NoPayload` is the typed empty variant; the coverage is
  CI-enforced (`TestEveryKnownEventTypeHasRegisteredPayload`) (§4). This is
  the Q2d "command envelope with correlated response" substrate: commands are
  typed events, responses are typed events, and the envelope carries a
  request id per §3.5.
- **Subscriber capability declaration.** The dashboard SPA is not the model
  — any consumer code-generated against the spec is. The eager point to pin
  in follow-up: how a parlay-side subscriber declares "accepts: navigate,
  prompt, toast" on the cargo of gascity's typed plane (a typed handshake
  riding the same contract), since Gascity itself does not need that
  declaration — it is the parlay-representation-plane semantic that Q2d asked
  about, layered on the adopted wire rather than invented beside it.

The five bespoke panel-aiming routes (`navigate`, `reload`, `device_cmd`,
`input_action`, `draft`) become ordinary command names on the adopted wire.
Round 4's request for an explicit sign-off on their deprecation window still
stands; the T-17 delta below now scopes that to the aliasing window on the
adopted plane, per the original recommendation.

---

## Q4 (REOPENED) · Storage — the JSONL-brute-scan default is superseded

Round 1's verdict (Q4, "LIGHT + CONFIGURABLE"): *"sensible smart default
(JSONL brute-scan), storage backend swappable later."* That default is what
this plane-split decision supersedes:

**Proposed replacement: the bead store's `Store` interface with the
`GC_BEADS=file` provider as parlay's storage path.** Verified:
`internal/beads/beads.go` defines the `Store` interface (CRUD + query +
metadata + molecule instantiation); provider resolution in `cmd/gc/main.go`
reads `GC_BEADS` first, then `[beads].provider`, defaulting to `"bd"`; the
`"file"` provider is `FileStore`, a JSON-file persistent store with atomic
writes (`engdocs/architecture/beads.md` §Architecture, §Configuration,
invariant 15). That gives parlay a storage layer with **no `bd`, no dolt, no
server, and no external dependency** — the Q4 "smart default without coupling
to PAI" intent, delivered by gascity's interface instead of parlay's own.

One precision correction to the brief, recorded per the "say it plainly"
rule: `FileStore` is not literally flock-free. `OpenFileStore` installs a Go
file flock on `<path>.lock` for cross-process safety when the filesystem is
the OSFS (`internal/beads/filestore.go:183-186`; `SetLocker` at :224), and
non-OS filesystems use a `nopLocker`. So the accurate claim is *no `bd`, no
dolt, no server, no external lock tool — a stdlib-grade file lock on a
sibling `.lock` file.* The substance of the supersession holds.

**Final verdict is left to the captain.** The gascity docs settle that the
interface and the file provider exist and pass a conformance suite, but they
do not settle whether parlay's chat-history archive/search rides
`GC_BEADS=file`, a parlay-owned JSONL archive, or a combination (see T-09 in
the re-cut). Q4 therefore moves back to **OPEN** in `00` with this proposed
replacement, carrying a "prior verdict superseded pending confirmation" note
— the confirmation is what decides T-09's substrate, and it is not this
round's call.

---

## Unchanged: Q2c and Q6b stay OPEN

- **Q2c** (pages as a generated-on-install sidecar: generator, source,
  destination) — **OPEN, untouched.** Gascity's own dashboard is the
  embedded SPA it serves as its `/` projection. That does not answer parlay's
  pages question; parlay's pages remain a representation-plane concern.
- **Q6b** (tailscale shape: tsnet embed vs `tailscale serve`) — **OPEN,
  untouched.** The connection layer is parlay's; gascity's adoption does not
  move it.

Both carry forward to the next round exactly as asked.

---

## Plan re-cut — what the plan-delta in `07` now means

`07`'s delta table amended `05`'s T-01…T-14 and added T-15…T-18. `07` is
append-only history; this section supersedes it for the tickets whose fate
the plane split changes. Re-cut per the rule: **DROPPED** = gascity supplies
the capability, **UNCHANGED** = parlay-plane work gascity does not supply,
**PENDING-VERIFY** = fate turns on the kill-switch check or on the Q4
captain confirmation. Every change carries a one-line reason.

| Ticket | Status | Reason |
|---|---|---|
| T-01 · Bun SSE leak patch (Q13) | UNCHANGED | Relay-wire concern on parlay's own surface; still downgraded until Bun retires. |
| T-02 · Frontend deploy (Q9/Q9a) | DONE | PR #106, merged and live (recorded in `07`). |
| T-03 · Port small product routes (Q2) | UNCHANGED | The port is parlay representation-plane work; gascity changes the wire style for the panel-aiming subset (T-17), not the fact that parlay's own product routes must land in go-server. |
| T-04 · TTS pluggable (Q2) | UNCHANGED | Still parlay's; now concretely "ship as a Gas City Pack" per Q2b instead of feeding a bespoke plugin system. |
| T-05 · Switch production to go-server; Bun off (Q1) | UNCHANGED | Parlay server lifecycle; gascity is a peer process, not a substitute for this port. |
| T-06 · Poll resume + client cursors (Q3/Q3a) | UNCHANGED | Parlay relay/cursor semantics; note gascity's cursor model (`Watch(ctx, afterSeq)`) is a design template, same spirit, different product surface. |
| T-07 · `parlay listen` direct; relay deleted (Q3) | UNCHANGED | Pure parlay relay work. |
| T-08 · Port `lavish-import`; delete CLI + parity harness (Q12) | UNCHANGED | Parlay CLI is the representation plane; gascity does not supply it. |
| T-09 · Message archive + search (Q4/Q4a) | **PENDING-VERIFY** | Substrate depends on the Q4 captain confirmation (GC_BEADS=file vs JSONL archive vs combo). |
| T-10 · Webhooks (Q8) | UNCHANGED | Outbound notification contract is parlay's; event source may widen to gascity-bus events, but the delivery surface stays parlay's. |
| T-11 · Beads health layer + public spawn (Q5/Q5a/Q10) | **DROPPED** (mechanics) | The bead store, session launching, liveness, and registry this ticket described are now gascity's (Store + Session providers + health patrol). Q5/Q5a's "beads is the layer" holds via gascity's store; Q5b's refuse-loudly ergonomics survive as the parlay-side `parlay spawn` > gascity-launch integration in the follow-up replan. |
| T-12 · Token + audit (Q6/Q6a/Q7/Q7b) | UNCHANGED | Auth and audit-ingest are parlay's security plane; gascity contributes design patterns (exec-credential contract) but not the feature. |
| T-13 · `tools/tui`, Bubble Tea (Q11) | UNCHANGED | Parlay front end (representation plane); can consume gascity's generated clients. |
| T-14 · Fold consensus into permanent docs | UNCHANGED | This round is part of it; the terminal-step PR remains. |
| T-15 · Plugin system (Q2/Q2b) | **DROPPED** | Q2b RESOLVED: a plugin IS a Gas City Pack. No bespoke manifest/verb-registration design to build; parlay authors packs and wires pack discovery into install. |
| T-16 · Pages as generated-on-install sidecar (Q2/Q2c) | UNCHANGED | Gated on Q2c, which stays OPEN; gascity's embedded dashboard is a different surface. |
| T-17 · Generic UI-command protocol (Q2/Q2d) | UNCHANGED (amended) | Q2d RESOLVED: ride gascity's typed HTTP+SSE plane + event registry + generated clients; the parlay-side work is the subscriber capability-declaration semantics and the five-route aliasing window. |
| T-18 · Gascity adoption — import + design ports (Q16) | **DROPPED** (superseded) | Premise superseded: parlay runs gascity rather than importing `pkg/eventexport` or hand-porting `internal/` designs. Replaced by the follow-up adoption ticket below (T-19). |
| **T-19 (new)** · Adopt gascity as execution plane (Q16) | **PENDING-VERIFY** | The actual build ticket (city scaffold, parlay-plane services over gascity's API/event bus, deploy story for the open gate). Held open by the kill-switch check and by the single-binary captain call. |

The worktree-teardown-safety port that Round 4 prized most (gascity's
six-gate ordering in `cmd/gc/bead_worktree_reaper.go`) is **not** discharged
by the plane split — parlay's `sweep`/`teardown` still run on worktrees
parlay spawned, so the design-port need survives and moves into T-19's
scope rather than vanishing.

---

## Round 6 needs from the captain

Three items, all deliberately unresolved:

1. **The open gate:** accept the deploy/shipping cost of ending the
   single-binary property (bundled city vs install-time `gc` vs documented
   prerequisite per Q5b).
2. **Q4's replacement verdict** (GC_BEADS=file vs JSONL archive vs combos for
   T-09's substrate) — the docs settle the existence of the option, not the
   choice.
3. **The kill-switch spike** for formula-v2 expression of #128 workflow-bead
   semantics; until it runs, T-19 stays PENDING-VERIFY.