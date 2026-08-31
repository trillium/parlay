# The position of the plane boundary

## Purpose and standing

This document states, capability by capability, where the Gas City ↔ parlay boundary falls:
what Gas City owns, what parlay owns, and the seam obligation parlay must meet for each split
to stay honest.

**It is a documentation artifact, not a contract.** It reads
[`docs/gascity-integration-contract.md`](gascity-integration-contract.md) (the binding
document) and does not re-open anything that document has already settled. Where this section
would contradict the contract, the contract wins; every capability below restates and cites
the binding ruling rather than re-deriving it. A bare `§N` cites that contract; a reference to
this document's own sections always names what it points at ("the register (§4)", "§3.2
below").

**It is documentation-only.** Nothing here changes Go, TS, or config. If a capability's split
turns out to require a code change to honor, that change is a separate work item, not part of
this document.

## The plane thesis

The working hypothesis that this document tests, capability by capability, is:

> **Gas City is the EXECUTION plane** — it owns process lifecycle, liveness, health, the event
> bus, dispatch, and the typed `/v0` HTTP + SSE transport.
>
> **parlay is the PROCESS + REPRESENTATION plane** — it owns the policies, frozen wire
> contracts, security boundary, owner/record rules, agent-facing representation, and the
> human/voice relay that no execution runtime is responsible for.

Most capabilities land cleanly on one side. A minority are **joints** — genuinely split
between the planes in a way that makes a single "owner" claim false. Every joint is entered in
the boundary register (§4), which records whether the split is **settled** (a binding ruling
already names both owners) or still **open**, and for every open item the evidence that would
settle it. A joint is not automatically unresolved: a split can be settled and still have two
owners.

## Naming note

**parlay's** subprocess launcher (`tools/parlay-bin/subprocess_spawn.go`, which contains no
Gas City code — §11) was renamed from `gascity` to `subprocess` in PR #133 (the
`gascity`→`subprocess` rename, merged 2026-08-29); `gascity` remains a deprecated alias. It is
not Gas City's own `subprocess` *provider*, which #133 did not touch — §10 flags that pair as a
naming collision.
Where a capability cites the launcher or its note file, the current name is `subprocess` and
the file is `docs/agent-notes/subprocess-launcher-a-herdr-free-escape.md`.

---

## 1. The execution plane — Gas City owns these

Each capability: **Owner** / **Why** / **parlay seam obligation** / **Evidence anchor**.

### 1.1 Sessions

- **Owner:** Gas City.
- **Why:** Gas City's `session` is the core mapping target for parlay's `agent` — §10's
  translation row: a Gas City session is bead-backed and survives supervisor restart, while a
  parlay agent is a directory under `~/.parlay/agents/<id>/`. §6 does not settle the mapping;
  it settles who may write the record. The spawn scope established Gas City's session runtime is
  the richer superset — parlay's launcher does not implement a session contract and never will,
  because Gas City already owns it.
- **parlay seam obligation:** the spawn seam (P9) binds to the Gas City session bead as the
  **sole writer** of its identity fields (id, agent name, worktree, project, bead binding,
  creation time) — §6(a).
- **Evidence anchor:** §10 `session → agent` row (the mapping); §6(a) of the binding contract (the sole-writer obligation); `docs/agent-notes/subprocess-launcher-a-herdr-free-escape.md` (the from-scratch port holds lifecycle semantics, not a session contract).

### 1.2 Process control

- **Owner:** Gas City's runtime/provider layer.
- **Why:** `Provider.Start/Stop/Interrupt` and the supervisor singleton own the process; the
  `subprocess` provider is Gas City semantics (detached `sh -c` child), and parlay's
  `subprocess` launcher (`tools/parlay-bin/subprocess_spawn.go`) is a from-scratch port of
  exactly those semantics — it contains **no Gas City code** (§11; the §11 comment-block
  correction landed in PR #132, and the `gascity`→`subprocess` rename in PR #133). Control
  verbs shell out with the verb's declared JSON flag (§5 — there is no persistent `--json` on
  the `gc` root) — the launcher is not a session owner, it is a boundary crossing.
- **parlay seam obligation:** the launcher-selection seam (`PARLAY_SPAWN_LAUNCHER`) and the
  spawn-sidecar flag record are parlay's side of the line; process ownership itself is not, and
  neither is teardown gate *ordering*. P10's obligation is to **adopt** Gas City's gate ordering
  and fail-closed posture while holding parlay's `hasUnpushed` + `isContentLanded` gate pair
  unchanged — §9.5: "P10 may reorder; it may not replace." Same ruling as §3.2 and register
  row 3.
- **Evidence anchor:** §5 HYBRID (control → shell-out); §10 translation rows `runtime provider →
  launcher`, `subprocess provider → the gascity launcher`; §11 (the launcher contains zero Gas
  City code).

### 1.3 Health / patrol

- **Owner:** Gas City's health patrol / controller.
- **Why:** Gas City reconciles `[[agent]]` declarations against running sessions in its
  singleton reconcile loop (`cmd/gc/city_runtime.go`). The liveness scope separates parlay's
  "only supervisor gap" — nothing schedules `parlay sweep` — from what Gas City does: patrol
  the sessions it already owns.
- **parlay seam obligation:** none for the patrol itself; parlay must keep its own
  liveness **verdict** machinery correct (see §3.1) — it is the boundary between Gas City's
  observation and parlay's representation.
- **Evidence anchor:** liveness scope (M — design port, not handover); §10 `supervisor`
  row: "parlay lacks a **session** supervisor, not process supervision."

### 1.4 The event bus

- **Owner:** Gas City's `internal/events` recorder, rotation, and `Seq`.
- **Why:** Gas City serialises every writer in a city through one append-only event log with
  gzip rotation (§8.3). parlay's `~/exchange/chat-history.jsonl` is live history and is
  explicitly **not** equivalent (§10). The bus itself belongs to Gas City under HYBRID
  (§5: liveness/event streams → typed `/v0` HTTP + SSE).
- **parlay seam obligation:** the **loud-skip cursor semantic** the events scope surfaced —
  `HistorySinceCursor`'s reset/skipped behaviour, which Gas City hooks silently floor — is a
  parlay-side contract (Q3a HYBRID CURSOR resolution). The `tail -F` CHAT_MSG chat relay stays
  parlay (see §3.4).
- **Evidence anchor:** §5; §8.3 (events seam owns the 250 ms shared write budget); §10
  `event Seq → cursor`, `.gc/events.jsonl → ~/exchange/chat-history.jsonl`.

### 1.5 Dispatch (order send / steering)

- **Owner:** Gas City.
- **Why:** Gas City's order/sling dispatcher steers a running session — terminal injection that
  parlay has no native equivalent for (`parlay send` is a chat POST the agent's own listen loop
  receives, explicitly *not* the same operation as Gas City `Nudge`; §10). Execution-plane
  steering belongs to Gas City under HYBRID control.
- **parlay seam obligation:** decide **when** to steer (policy, §3.4) and surface the result to
  the human; the mechanical delivery of the order to the session is Gas City's.
- **Evidence anchor:** §10 `Nudge → (nearest: parlay send)` row; §3.4 capability policy.

---

## 2. The process + representation plane — parlay owns these

### 2.1 Staleness / agent collection

- **Owner:** parlay.
- **Why:** Gas City has **no** staleness/wedge policy. The `stale`/`sweep` hold-guards each
  came from a real incident; the four incident-derived guards are parlay's. Gas City's bead
  worktree reaper is a *reclaimer*, not a staleness policy: it is default-off, it never
  enumerates agents, and where it can reach a parlay worktree at all the outcome is governed
  by §9.5, not by anything that decides an agent is finished. So the oracle can move without
  the collection policy moving. Nothing in Gas City supersedes `parlay sweep`/`stale`.
- **parlay seam obligation:** unchanged — keep the guards; `parlay sweep [--apply]` remains the
  only collector that can see parlay agents.
- **Evidence anchor:** liveness scope (M); §8.2; AGENTS.md "Verbs that exist because a naive
  command lies" (`parlay sweep`).

### 2.2 The frozen liveness wire contract (`crew-state`)

- **Owner:** parlay.
- **Why:** `crew-state` exit codes 3/4/5/6 and the three source suffixes are a **frozen wire
  contract** (§8.2), consumed by `parlay sweep` and `parlay stale`. Four guards, four real
  incidents. This is parlay representation that rides on whatever liveness oracle Gas City
  provides — moving the oracle does not move the verdict contract.
- **parlay seam obligation:** the status seam (P6-adjacent) may not change the exit codes or
  the three strings; anything new is a new channel.
- **Evidence anchor:** §8.2 (BINDING); `tools/cli/internal/commands/crew_state.go:96-101`,
  `:233-244`.

### 2.3 Supersession / drift policy

- **Owner:** parlay.
- **Why:** the topology scope found `gc formula version-check` detects drift but there is no
  migrate/supersede/severity **policy** — Gas City surfaces the fact of drift, it does not
  decide what parlay does about it. That policy is parlay's to define (and currently
  includes the migrate/supersede gap).
- **parlay seam obligation:** define the migrate/supersede/severity policy; Gas City provides
  the drift signal, not the decision. **Met** by `tools/cli/internal/supersession` and
  [supersession.md](supersession.md) (task-4cfpv.13).
- **Evidence anchor:** topology scope (Bucket C6 formula/supersede gap) named the gap; the
  supersession policy unit settled it — see register row 4.

### 2.4 The security boundary (ingress)

- **Owner:** parlay.
- **Why:** `POST /api/chat/events` is parlay's **out-of-process ingress seam**, allowlisted
  one name per real producer, default one entry (`tool_event`) — §8.5 HARD BOUNDARY. The
  `GUARDED_CHAT_PATHS` registry and the no-auth chat API rule are parlay's. No execution
  runtime is responsible for parlay's ingress security.
- **parlay seam obligation:** a new ingress producer is a policy decision, not a wiring
  detail; `JSON_EXEMPT_PATHS` is a closed three-member list. The events seam may not widen the
  allowlist.
- **Evidence anchor:** §8.5; `packages/go-server/internal/handlers/events_ingress.go`; §10
  `[events.export]` → `POST /api/chat/events` row (parlay's ingress **must not widen**).

### 2.5 Owner / record rules

- **Owner:** parlay (as policy); the spawn seam is the sole writer against the Gas City bead.
- **Why:** §6 is a **parlay policy decision** about who may write the agent record — it
  constrains parlay's own seams, and it chooses the Gas City session bead as the substrate.
  The *substrate* (JSONL vs beads Go import) is still open (§6(d), Q4); whichever wins, the
  **single-writer rule** is parlay's to hold.
- **parlay seam obligation:** spawn creates exactly one bead; status writes state and **never
  creates**; an absent bead is an error to report, not a bead to mint; fail-open posture from
  `worklink.go` is load-bearing.
- **Evidence anchor:** §6(a)–(d); `tools/cli/internal/identity/worklink.go:75-85`.

### 2.6 Voice + human relay

- **Owner:** parlay.
- **Why:** the channel split (Q16) assigns TTS and the human/voice relay unconditionally to
  parlay's representation plane. Gas City's `Nudge` is terminal injection and is **not** `parlay
  send`; the two must not be mapped 1:1 (§10). The chat spool / relay is live history parlay
  must not clobber.
- **parlay seam obligation:** keep TTS under `$PAI_DIR`, keep the chat relay on parlay's side,
  and never treat `Nudge` as the voice channel.
- **Evidence anchor:** §10 `Nudge → (nearest: parlay send)` row ("Lossy and dangerous").
- **Source of authority:** Q16 channel split (topology scope).

### 2.7 Routing-with-confidence policy

- **Owner:** parlay.
- **Why:** Gas City's `AddressDirectory.ResolveAddress` *refuses* an ambiguous address rather
  than picking a winner (§10) — the execution plane's guarantee. But "routing with confidence /
  progressive hardening" — the policy about when parlay may act on a resolved address and when
  it must hold — is parlay-representation, explicitly assigned to parlay in the topology scope,
  §4.3. The refusal mechanism is Gas City's; the confidence policy is parlay's.
- **parlay seam obligation:** express the confidence policy; use Gas City's refusal as the
  enforcement backstop, not as the whole decision.
- **Evidence anchor:** §10 `AddressDirectory.ResolveAddress → agent-id lookup`; topology
  scope §4.3.

---

## 3. Joints — genuinely split between the planes

These do not assign to one plane. Each names the split precisely and either the binding ruling
that already settles it or, where it is open, the evidence that would. The register (§4)
carries the status for each; §3.1 is register row 1, §3.2 rows 3 and 7, §3.3 row 5, §3.4 row 6.

### 3.1 Liveness oracle vs liveness verdict

- **The split:** Gas City can provide the *observation* (an `IsRunning`/`ProcessAlive`-style
  truth, or a `/health` probe). The *verdict* — whether the agent is `status`, `status-unenrolled`,
  or `status-degraded`, and the frozen exit code that results — is parlay representation and
  must not move.
- **What settles it:** the shadow-ordering rule (§7) already sequences this: P7 (liveness,
  [SHADOW]) runs the oriented seam behind a flag feeding the same consumer, then flips the
  oracle while the verdict contract stays parlay's. Until P7 flips, parlay's registry ∩
  process-table rule (robots-jkwc) is the oracle and is authoritative.
- **Not a clean single owner.** Gas City owns the clock; parlay owns the gauge.

### 3.2 Safety gates / teardown ordering

- **The split:** Gas City's *ordering + posture* teardown gates are adopted (P10), and Gas
  City has its own reclaimers — the two §9.5 reclaimers reach **opposite verdicts on
  landability** for the same unpushed parlay branch, and §9.5's correction is to the *cost* of
  that disagreement: a disappearing checkout, not destroyed commits.
  But parlay's `hasUnpushed` + `isContentLanded` gate pair stays **unchanged** and binding
  (§9.5). Within that pair the strength is `isContentLanded`'s: its tree comparison is "the
  stronger of the three" and beats *both* Gas City gates (§9.5, §13 row 7), while `hasUnpushed`
  is byte-equivalent to `HasUnpushedCommitsResult` and carries that gate's known weakness. §9.5
  forbids weakening the pair to match either Gas City gate and forbids adopting
  `HasUnreachableCommitsResult` in its place — it does not license leaning on `hasUnpushed`.
  `--force` semantics split further, and that sub-question is **open, not settled**: nothing in
  the contract rules on `--force` — its one mention (`:825`) is `gc supervisor install` rollback
  — while parlay's own force paths today waive the uncommitted-work and unpushed-but-unlanded
  refusals with a warning, and the no-launch-spec hold plus the per-task-spawn proof. Which
  refusals stay unconditional under Gas City's posture is register row 7.
- **What settles it:** P10 (teardown ordering) fixes which gate runs first and which side owns
  the veto, and must state the `--force` bypass matrix explicitly (row 7); §9.5 names the danger
  (the weaker gate lets a local branch vouch for itself and removes the checkout) and bounds it
  (the branch ref survives, so the work is recoverable).
- **Not a clean single owner.** Gas City owns the gate order; parlay owns the landability proof.
- **Evidence anchor:** §9.5 (BINDING); `tools/cli/internal/commands/teardown.go:124-146`
  (the two force-waivable refusals); `tools/cli/internal/commands/sweep.go:163`, `:182` (the
  no-launch-spec hold and the per-task-spawn proof waiver).

### 3.3 Transport

- **The split:** "transport" is ambiguous across the boundary. The typed `/v0` HTTP + SSE is
  Gas City (session transport, durable cursors, LAST EVENTS). The **relay singleton** — one per
  runtime dir, bound to one server, unix-socket-capped — is parlay infra with a reserved
  canonical runtime dir. And the human chat relay (`tail -F` CHAT_MSG) is parlay representation.
- **What settles it:** a future seam must name *which transport* it means. The durable-cursor
  transport is Gas City's seam; the relay is a **parlay-deployment** concern and stays under
  parlay's ownership regardless of who carries the bytes.
- **Not a clean single owner.** Gas City carries session bytes; parlay owns the relay singleton
  and the human chat pipe.
- **Evidence anchor:** §5 HYBRID (liveness/event streams → typed `/v0` HTTP + SSE); the
  contract's §3, the vendored OpenAPI artifact (`Last-Event-ID` / `after_cursor` durable
  cursors, `:240-242`); §10 translation row `event
  Seq → cursor`; for the parlay side, `docs/agent-notes/the-relay-is-a-per-runtime-robots-buu8.md`
  (per-runtime-dir singleton bound to one server, 104-byte socket cap) and
  `docs/agent-notes/the-canonical-runtime-dir-is-reserved-robots-93xu.md` (the reserved canonical
  runtime dir).

### 3.4 Capability declaration vs capability policy

- **The split:** Gas City *reports* capabilities (`Capabilities()`, `ProviderCapabilities`).
  The *policy* — what parlay will and will not do with a reported capability — is parlay's. The
  spawn scope's R7 is the concrete case: parlay must **refuse to steer** a session whose
  provider reports no injection channel. Gas City reports "no injection channel"; parlay
  decides that means "refuse to steer".
- **What settles it:** the seam that consumes a capability report is parlay's policy layer;
  the report itself is Gas City's. The owner of the *decision to act* is the owner of the
  capability — and that decision is parlay's.
- **Not a clean single owner.** Gas City is the source of the fact; parlay is the source of the
  meaning.

---

## 4. The boundary register — every split, its status, and what settles each

| # | item | status | unresolved because | what settles it |
|---|---|---|---|---|
| 1 | liveness oracle source | **joint (open)** | oracle can move to Gas City (P7) but verdict contract stays parlay (§3.1); which `.go` probe is authoritative today is ad hoc | P7 shadow flip closes the oracle question; the verdict contract is already closed (§8.2) |
| 2 | `crew-state` verdict provenance | **settled** | — | §8.2 BINDING: exit codes + source suffixes frozen, new channels only |
| 3 | safety-gate split (`hasUnpushed` + `isContentLanded`) | **settled split** | — the ruling is closed; only its *implementation* is pending | §9.5 BINDING already settles it: parlay's `hasUnpushed` + `isContentLanded` pair stays unchanged and unweakened, gate *order* moves to Gas City. P10 implements that ruling; it does not decide it |
| 4 | supersession / drift severity policy | **settled (parlay-owned)** | — | landed as `tools/cli/internal/supersession` + [supersession.md](supersession.md) (task-4cfpv.13): SemVer-classified severity, reprocessing requirements, captain visibility; `gc formula version-check` remains the raw signal this policy consumes |
| 5 | transport territory | **joint (open)** | "transport" means `/v0` (Gas City) to one seam and relay-singleton (parlay) to another (§3.3) | a seam naming *which* transport it means; parlay's relay singleton stays parlay regardless |
| 6 | capability policy (R7) | **settled split** | — both owners are named | spawn scope R7: refuse to steer on a no-injection-channel provider; report is Gas City's, decision is parlay's |
| 7 | `--force` bypass matrix under Gas City's posture | **open** | §9.5 rules on the gates and their ordering but not on which refusals `--force` may waive; the contract's only `--force` mention (`:825`) is `gc supervisor install` rollback, and parlay's force paths today waive uncommitted work, unlanded commits, the no-launch-spec hold, and the per-task-spawn proof | P10 must state the matrix explicitly — which refusals `--force` waives and which are unconditional — because no binding ruling does today |

Three of the seven are open (**#1**, **#5**, **#7**); rows 3 and 6 are *settled splits*
— two owners each, but the ownership question itself is closed, so they are not unresolved. Of
the open three, **#1** and **#5** are *self-limiting* — they resolve to "the oracle is whichever
probe the currently-shadowed P7 flips" (#1) or "the relay stays parlay regardless of which
transport a future seam means" (#5), so they are permanent unknowns only if nobody files the
seam; and **#7** is open because no binding ruling covers `--force` at all. Row **#4** closed
when the supersession policy landed on the parlay side (see the row).

---

## 5. Cross-cutting binding restatements (from the contract)

- **No version guarantee (§4).** `requires_gc` is parsed, preserved, and **never compared**.
  parlay must never rely on it; any version floor parlay needs is its own named-error check.
- **HYBRID (§5).** Control verbs → shell-out with the verb's declared JSON flag; liveness and
  event streams → typed `/v0` HTTP + SSE. The Go-library import mode is **closed** — a language
  rule, not a preference (§5).
- **Agent record owner (§6).** Spawn creates exactly one bead; status writes state and never
  creates; an absent bead is an error to report, never a bead to mint. Substrate (Q4) is open.
- **Ordering rule (§7).** READ BEFORE WRITE. OBSERVE BEFORE CONTROL. The P0→P13 chain is
  binding ordering, not a unit inventory; P2, P3, P5, P8 are unallocated and nothing here
  reconstructs one from the bead tree.
- **Ingress must not widen (§8.5).** `POST /api/chat/events` allowlist stays one-name-per-real-
  producer; a new producer is a policy decision.
- **Crew-state is frozen (§8.2).** No new exit codes, no new source suffixes; new meaning goes
  on a new channel.

---

## 6. Maintaining this document

Keep this file current with the binding contract. When a P0–P13 unit flips an item in the
register (§4) from *open/joint* to *settled*, update the row and the owning section rather than
leaving a stale "unresolved". When the contract itself changes a ruling, re-run each capability
here against it — the contract is authoritative and this file follows it, never leads it.
