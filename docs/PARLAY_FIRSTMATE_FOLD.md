# Migrating firstmate's setup mechanics down into parlay (the "fold")

> **Reading this from outside?** This document is internal design work about
> integrating parlay with **firstmate**, the author's private agent-fleet
> supervisor, and it assumes several other unreleased tools (PAI, herdr, the
> `beads` stores). None of them are public, and nothing described here is required
> to run parlay — see [`../README.md`](../README.md) for that. It is kept in the
> open because the reasoning about agent supervision is real, not because you are
> expected to reproduce the setup.

> **Status:** DESIGN — awaiting review. No code written yet.
> **Ownership direction (decision-3ae, §0):** parlay OWNS the agent-setup MECHANICS
> as native primitives (spawn / identity / channel / status+supervision / worktree /
> teardown / harness); firstmate keeps only POLICY (delivery mode + merge discipline +
> escalation judgment) and shrinks to THIN WRAPPERS over parlay primitives — not a
> second stack. The fold **migrates firstmate's mechanical intelligence DOWN into
> parlay** so every launcher inherits supervision (fixing completion-blindness).
> **Goal:** A parlay agent keeps parlay's strengths (panel-enrolled, phone-reachable,
> monitor armed, durable identity/handoff memory) AND gains the lifecycle mechanics
> (structured brief, keyed status, worktree isolation, recorded meta, safe teardown,
> model/effort, delivery mode) **as parlay-owned capability**, not a firstmate island.
>
> **Addendum folded in (robots-5cz / -0w9):** a **doc-launch-method verifier** (§8)
> that enforces agent docs point to the `identity` + `parlay monitor` + `parlay-spawn`
> CLI surfaces, not raw `curl`/bearer-token API writes — smallest form a
> failing-on-drift test, fuller form a `parlay verify-agent-docs` / `parlay doctor`
> subcommand. The pulse-agent skill's **canonical home moves into this repo**
> (`skills/pulse-agent/`) and is **symlinked into PAI** (§8.6), so the loaded skill
> and the CI-scanned doc are one file. Canonical startup is **three steps**:
> `identity` (recover self) + `parlay monitor --agent <id>` (arm channel) +
> `projects show <bead>` (load your canonical project record) — `identity` alone
> does NOT arm the monitor.
>
> **Addendum 2 folded in:** a spawned agent **owns and maintains its canonical
> `projects`-store bead** (§3.9). The bead is resolved at spawn (`--project <id>`,
> reusing `project-match` with a parlay-native fallback), recorded as `project_bead=`
> in meta, carried in the prompt, and the launch contract's third startup step loads
> it (`projects show <bead>`); the agent updates it on milestones — the same
> transitions that fire `parlay status`.
>
> Sources read first-hand: `bin/parlay-spawn`, `packages/cli/src/commands-identity/*`,
> `packages/cli/src/monitor.ts`, `packages/server/src/agent-context.ts`, the
> `pulse-agent` skill; and firstmate's `AGENTS.md` §2/§4/§6/§7/§8/§11 plus
> `fm-spawn.sh`, `fm-brief.sh`, `fm-watch.sh`, `fm-crew-state.sh`,
> `fm-classify-lib.sh`, `fm-teardown.sh`, `fm-harness.sh`.

---

## 0. Ownership direction — the end-state (decision-3ae)

**This fold is a MIGRATION, not an integration.** Direction set by decision-3ae
(Trillium + mayor, 2026-07-21): **parlay OWNS the agent-setup MECHANICS as native
primitives** — spawn, identity, channel, status/supervision, worktree, teardown,
harness, event. **firstmate keeps only POLICY** — what to spawn, routing, delivery
mode + merge discipline (captain-merge / never-destroy-unlanded-work), and
escalation judgment. The mechanical intelligence that currently lives *only* in
firstmate's shell (`fm-watch`, `fm-classify-lib`, `fm-teardown`'s safety checks,
the brief contract, worktree isolation, turn-end hooks) **migrates DOWN into parlay
as reusable capability**, and firstmate's `fm-spawn`/`fm-watch`/`fm-teardown` shrink
to **thin policy wrappers** that compose parlay primitives — **not a second spawn
stack** (canonical-home rule: one substrate).

Why this direction (and why it fixes the completion-blindness this session hit):
when supervision is a **parlay-native** capability, **every launcher inherits it** —
not just firstmate. A panel agent spawned by anyone becomes observable/superviseable
because the primitive lives in the substrate everyone already calls. Reusing
firstmate's loop (former §3.6.1) is therefore a **transitional bridge**, not the
destination; the destination is parlay owning the supervision primitive that
firstmate merely *wraps with policy*.

**The migration ledger** (from decision-3ae — this is the fold's scope map):
| Bucket | Items | Where it ends up |
|---|---|---|
| **MECHANISM → migrate to parlay** | event-driven supervision (wake-on-status-change + authoritative run-state read), **unattended/headless supervision** (a daemon owns the loop with no supervisor session present: self-handle routine wakes, batch escalations, honor a max-defer bound, mark injected messages with an in-band captain-return sentinel — §3.6.2), structured brief contract, per-task worktree isolation, SAFE teardown (never discard unlanded work), keyed open/resolved status protocol, harness adapters + turn-end hooks | **parlay-native primitives** |
| **POLICY → stays firstmate** | delivery modes + merge discipline + captain-merge rule, escalation/what-to-spawn judgment, **away-mode policy** (the `/afk` enter/exit gesture, the max-defer bound value, what counts as a captain-relevant escalation, and **approval-authority preservation** — an unattended daemon never approves a merge/irreversible action for the absent captain; it batches and waits — §3.6.2) | firstmate (thin wrappers over parlay) |
| **MIXED → split case-by-case** | backlog / fleet-state / recovery / secondmates | mechanism half → parlay; judgment half → firstmate |

The section numbering below (§3 KEEP/ADAPT/DROP, §6 slices) predates this decision
and is now read through it: a "DROP" means *not parlay's job at all*; anything that
was firstmate-owned mechanism is an **ADAPT = build-as-parlay-primitive**, sequenced
by the slices. §3.4 (harness) is the one item re-scoped by decision-3ae — see there.

---

## 1. The one-paragraph thesis

Parlay already owns *identity, reachability, and self-restart*: an agent is a
durable, phone-reachable chat tab that remembers itself across context resets.
Firstmate today *additionally* owns *supervised task lifecycle*: a structured brief,
a machine-classifiable status stream, worktree isolation, and never-destroy-unlanded
teardown. **decision-3ae says those lifecycle MECHANICS belong in parlay too** — so
the end-state is one substrate that owns both identity AND lifecycle mechanics, with
firstmate reduced to the policy brain that composes them. The near-term work still
*starts* by reusing firstmate's loop (fastest bridge), but each primitive is built to
LIVE in parlay and be inherited by every launcher. The one genuine runtime overlap
(status vs. monitor) resolves cleanly because they point opposite directions:
parlay's Monitor is **inbound** (captain → agent), the status stream is **outbound**
(agent → supervisor) — and in the end-state that supervisor is a parlay primitive.

---

## 2. Side-by-side: what each system already is

| Dimension | parlay-spawn (target) | firstmate (spec) |
|---|---|---|
| Spawn surface | `parlay-spawn <id> <name> <color> <prompt> [--cwd] [--focus] [--model]` or `--ephemeral <prompt>` | `fm-spawn.sh <id> <repo> [--scout|--secondmate] [--harness] [--model] [--effort] [--backend]` + batch |
| Runtime | one **herdr** terminal, top-level env-scrubbed `claude`, YOLO (`--dangerously-skip-permissions`) | pluggable **backend** (tmux ref / herdr / zellij / orca / cmux) × **harness** (claude/codex/opencode/pi/grok) |
| Isolation | none — runs at `--cwd` (default `$HOME`) | **mandatory** git worktree via `treehouse`, asserted distinct from primary checkout |
| Recorded state | `~/.parlay/agents/<id>/` : `context.json` {id,name,color}, `identity.md` frontmatter (launch spec: id,name,color,cwd,model,ephemeral,mode,effort,kind,yolo,worktree,project — lifecycle fields omitted when not provided), `session-start` sentinel | `state/<id>.meta` (key=value): window, worktree, project, harness, model, effort, kind, mode, yolo, tasktmp (+ backend fields, +pr/pr_head, +secondmate home/projects) |
| Task instructions | a big inline **enrollment prompt** (arm Monitor, reply, recover, shutdown protocol) + the raw task string | `data/<id>/brief.md` — structured contract: Role, Task, Setup (worktree assertion + branch), Rules×7, Project-memory, Definition of done (mode-shaped) |
| Inbound channel | `parlay monitor --agent <id>` (relay-backed `tail -F`, `CHAT_MSG` lines) — **captain → agent** | steering via `fm-send.sh` (one line into the pane) |
| Outbound channel | `reply` → panel (human prose); `scratchpad`/`identity`/`handoff` (durable memory) | append `<verb> [key=<slug>]: <note>` to `state/<id>.status` — **agent → supervisor** |
| Supervision | a **human** reads the chat tab | event-driven **watcher** (`fm-watch.sh`) wakes a supervisor agent only on an *actionable* status change, reconciling `.status` (event log) against `fm-crew-state.sh` (authoritative current-state) |
| Delivery model | "do the task, reply when done" — no defined DoD | `no-mistakes` / `direct-PR` / `local-only` (ship) or `scout` (report); `yolo` decides who approves |
| Shutdown | self-restart: `handoff` create → `identity --submit` (atomic, resets context); ephemerals GC'd by `--reap-ephemeral` | `fm-teardown.sh <id> [--force]` — **refuses to destroy unlanded work**, returns worktree to pool, kills window, deletes state |
| Nature of an agent | persistent, self-healing peer | disposable crewmate (or persistent secondmate) |

---

## 3. Convention-by-convention: KEEP / ADAPT / DROP

Each firstmate convention, mapped onto parlay. "KEEP" = parlay already has an
equal; "ADAPT" = port the idea in parlay's idiom; "DROP" = out of scope for the
fold (parlay's constraints make it unnecessary).

### 3.1 Structured brief — **ADAPT** (slice 1)
Firstmate composes `brief.md` with a fixed anatomy. Parlay today jams enrollment
+ task into one prose blob. Fold: **keep parlay's enrollment block verbatim**
(it's load-bearing — Monitor arming, recovery chain, shutdown protocol), and
**append a firstmate-shaped task contract** below it:

```
## Task
<the task>

## Definition of done
<delivery-mode-shaped; see 3.5>

## Status protocol
Append a keyed status line at each supervisor-actionable transition:
  parlay status working|needs-decision|blocked|paused|done|failed "<one line>"
Report sparsely — each status wakes your supervisor. `reply` is for the human;
`status` is for the machine. (A `done`/`failed` is terminal.)

## Setup            (only when --worktree)
<worktree-isolation assertion + branch step; see 3.3>
```

Parlay's enrollment prose and firstmate's brief contract are **complementary, not
redundant**: enrollment tells the agent how to be reachable and how to recover;
the brief tells it what "done" means and how to signal progress. Neither subsumes
the other.

### 3.2 Recorded meta — **ADAPT** (slice 1)
Parlay already writes a per-agent home (`~/.parlay/agents/<id>/`) with a launch
spec in `identity.md` frontmatter. Firstmate's `meta` is a **superset**: launch
spec + runtime facts. Fold: **extend the existing frontmatter** (do not add a
second file — one canonical home per agent) with firstmate-derived fields:

| add to identity.md frontmatter | value |
|---|---|
| `kind` | `ship` \| `scout` \| (parlay's default) `ephemeral`/`named` |
| `mode` | `report` \| `branch` \| `pr` (parlay's delivery vocabulary, 3.5) |
| `yolo` | `on` \| `off` (parlay-spawn already launches YOLO; record it) |
| `effort` | effort level or `default` |
| `worktree` | absolute worktree path (only when `--worktree`) |
| `project` | absolute repo root the worktree derives from |
| `project_bead` | canonical `projects`-store bead id the agent owns (§3.9) |
| `brief` | path to the seeded brief, for re-inspection |

Rationale: `identity --launch <id>` already reconstitutes an agent from this
frontmatter; adding these fields means a **relaunch restores the full lifecycle
context**, not just id/name/color. This is parlay's `identity --launch` and
firstmate's meta becoming the same record.

### 3.3 Worktree isolation — **ADAPT, opt-in** (slice 2)
Firstmate makes worktree isolation *mandatory* and provisions it via `treehouse`.
Parlay agents run top-level at `$HOME` and often aren't in a repo at all, so
mandatory isolation is wrong here. Fold: add **`--worktree`** (default OFF). When
set:
- create a plain `git worktree add <repo>/.worktrees/parlay-<id> --detach`
  (parlay already has a `.worktrees/` dir and uses worktrees); use `treehouse`
  **only if present on PATH**, else fall back to `git worktree` — do not hard-depend
  on firstmate-only infra.
- seed the brief's `## Setup` with firstmate's **worktree-isolation assertion**
  (`pwd -P` + `git rev-parse --show-toplevel` both resolve to the worktree, not
  the primary checkout; STOP + `blocked:` if not) and a branch step
  (`git checkout -b parlay/<id>`).
- record `worktree=`/`project=` in the frontmatter (3.2), which arms safe teardown
  (3.7).

The brief's isolation assertion is the *brief-time* guard; the *runtime* backstop
for the same failure mode (an agent that branched/committed in the PRIMARY checkout
instead of its worktree, stranding the primary on a feature branch) shipped as the
**`parlay guard`** verb (contraction **C4**, ported from firstmate's `fm-guard.sh`
per `AGENTS.md §8`). It is advisory-only — WARNS with a bordered banner + a
non-destructive `git checkout <default>` restore, never blocks — and is wired into
the variant lifecycle: `parlay variant launch`/`teardown` call `guardRepo(<primary>)`
so a stranded primary surfaces on the next fleet action. It also carries a
`--beat`/liveness beacon so a missing watcher heartbeat is alarmed while variants
are in flight. See `parlay guard --help` and `commands-guard.ts`.

### 3.4 Harness / model / effort — **DEFERRED parlay-native primitive** (re-scoped by decision-3ae)
decision-3ae lists **harness adapters + turn-end hooks** as MECHANISM to migrate to
parlay — so this is *not* a permanent DROP, it is a **deferred parlay-native
primitive**. The end-state: parlay owns a harness-adapter primitive (the launch
template + turn-end-hook install per agent CLI), so any launcher can pick a harness,
exactly as firstmate's `fm-harness.sh` + `launch_template()` do today — just living
in parlay. Sequencing:
- **`--model`** — already exists. KEEP.
- **`--effort`** — ADD as a pass-through to claude's effort flag, recorded in meta.
- **harness adapters (codex/opencode/pi/grok) + turn-end hooks** — **build LAST**
  (post-slice-3). Parlay launches `claude` today, so the multi-harness primitive has
  no consumer yet; scaffold the seam now (a single `launch_template(harness)` +
  `install_turnend_hook(harness)` indirection point in the launcher, claude-only)
  so adding an adapter later is filling a table, not a refactor. This is the parlay
  home for firstmate's `fm-harness.sh` knowledge when it migrates.
- **pluggable session backend (tmux/zellij/orca/cmux)** — **DROP** (genuinely, not
  deferred). Parlay is herdr-only by design; unlike harness, decision-3ae does not
  ask parlay to own multiple session backends. `fm-backend.sh`'s multi-backend verb
  layer stays firstmate's problem if it ever needs tmux; parlay's substrate is herdr.

#### 3.4a Crew-dispatch profiles + quota-balanced selection — **STAYS-FIRSTMATE POLICY (re-activates with the harness primitive)**
Firstmate's `config/crew-dispatch.json` + `bin/fm-dispatch-select.sh` implement the
per-task *selection* of harness/model/effort, including the deterministic
quota-balanced load-balancing described in firstmate `AGENTS.md` §4 (round-robin a
task across eligible harnesses weighted by remaining quota, deterministically so two
supervisors pick the same target). This is **POLICY — the what-to-spawn decision —
and it correctly STAYS FIRSTMATE under decision-3ae**: parlay owns the harness
*mechanism* (the adapter primitive, §3.4), firstmate owns the *choice* of which
harness/model/effort a given task gets. It is **not a contraction** — parlay never
claimed this decision, and the fold does not move it.

Two things this fold makes explicit so the capability is not silently lost:
- **Retention is now stated.** Crew-dispatch (`crew-dispatch.json` +
  `fm-dispatch-select.sh`) is firstmate-retained policy. It composes parlay's future
  harness-adapter primitive (§3.4) via `parlay-spawn --harness/--model/--effort` once
  those land — firstmate selects, parlay executes. No parlay-side reimplementation.
- **Re-activation is sequenced.** The quota-balanced multi-harness path is **inert
  until the deferred harness-adapter primitive ships** (§3.4, built LAST): with parlay
  claude-only, `fm-dispatch-select.sh` has exactly one eligible target and the
  balancer is a no-op. When the harness primitive lands and adds real
  codex/opencode/pi/grok targets, crew-dispatch re-activates as-is — it selects,
  passes `--harness` through to the new primitive, and load-balancing becomes live.
  The seam scaffolded in §3.4 (`launch_template(harness)`) is exactly the consumer
  crew-dispatch will drive; **track this re-activation against the §3.4 harness
  milestone**, not as a slices 0–3 deliverable.

The `--effort` / `--model` pass-throughs (§3.4) are the slice-1 down-payment on this
seam: they let crew-dispatch's model/effort *dimensions* work today, with only the
harness *dimension* waiting on the deferred primitive.

### 3.5 Delivery mode + yolo — **ADAPT (thin)** (slice 1 for `report`, slice 2 for `branch`/`pr`)
Firstmate has three ship modes + scout, and an orthogonal yolo. Parlay's agents
are conversational, so start minimal:
- **`report`** (default) — DoD = "do the task and `reply` your result; `status done`."
  This is parlay's current behavior, now with a named DoD. No worktree needed.
- **`branch`** — DoD = "commit on `parlay/<id>`, `status done: ready in branch`."
  Requires `--worktree`.
- **`pr`** — DoD = "push + open PR via `gh`, `status done: PR <url>`." Requires
  `--worktree`.
- **yolo** — parlay-spawn *already* launches every agent in skip-permissions YOLO,
  so `yolo=on` is the de-facto default; just **record** it in meta. Firstmate's
  "yolo decides who approves the merge" only matters once a supervisor is doing
  merges (a later slice); until then it's a recorded fact, not a behavior.

Deliberately **not** porting `no-mistakes` mode — that's firstmate's validation
pipeline, out of scope.

### 3.6 Status reporting + supervision — **ADAPT: the heart of the fold** (slice 1 = emit)
This is the one real overlap, and it resolves by direction (see §4). Fold:
- **ADD a thin `parlay status` verb** (sibling to `reply`/`scratchpad`/`identity`)
  that appends firstmate's exact grammar — `<verb> [key=<slug>]: <note>` —
  keyed off `PARLAY_AGENT_ID`. Verb vocabulary ported verbatim: `working`,
  `needs-decision`, `blocked`, `paused`, `done`, `failed`, and `resolved [key=…]`.
- **The sink is env-configurable** (this is what makes 3.6.1 work): the verb writes
  to `$PARLAY_STATUS_FILE` when set, else defaults to `~/.parlay/agents/<id>/status`.
  So the *same* verb feeds parlay's own store OR firstmate's supervision, depending
  on who launched the agent — the agent code is identical either way.
- **KEEP `reply`** unchanged — it stays the human-prose channel.
- **Recommended north-star:** `parlay status <verb> "<note>"` *also* posts a
  human `reply` mirror in one call (one agent action → two renderings: keyed line
  for the machine, prose for the panel). Start as two calls if simpler; unify once
  proven.

#### 3.6.1 Completion signal — NEAR-TERM BRIDGE (reuse firstmate) → END-STATE (parlay primitive)
Two horizons, per decision-3ae:

**End-state (decision-3ae):** supervision is a **parlay-native primitive** — a
`parlay supervise`/`crew-state` capability that wakes on the keyed status stream and
reads authoritative run-state. Every launcher inherits it; firstmate's `fm-watch`
becomes a thin policy wrapper (escalation judgment) over it. This is where §3 of the
slices lands.

**Near-term BRIDGE (ship first):** don't block the completion signal on building that
primitive. firstmate's mechanism *already exists* — `state/<id>.meta` registration +
`state/<id>.status` append + `fm-classify-lib.sh`'s keyed contract + the `fm-watch.sh`
loop. So the *first* completion signal **reuses firstmate's loop directly** as a
bridge, buying the behavior immediately while the parlay-native primitive is built
underneath it. (Still NOT the question-hx9 store-event fabric — §2.5 of
`CLI_VERBS_AND_EVENTS.md`, `robots→mechanic`/`request-close→notify`, is a separate
larger thing.) The migration: bridge reuses firstmate → parlay grows the native
supervise primitive → firstmate's loop shrinks to wrap it.

The whole near-term path, when **firstmate launches a panel agent** (firstmate
wraps / invokes `parlay-spawn`):
1. **firstmate registers a `state/<id>.meta`** in its home for the panel agent —
   the same meta record any crewmate gets, so the agent enters firstmate's fleet as
   a supervised task (window/worktree fields can be panel-appropriate or empty).
2. **firstmate injects `PARLAY_STATUS_FILE=$FM_HOME/state/<id>.status`** into the
   spawn env (parlay-spawn passes env through today), and the brief tells the agent
   to `parlay status <verb> …` on each transition (`done`/`blocked`/`needs-decision`/
   `paused`). The verb appends to firstmate's status file (per the env sink above).
3. **firstmate's existing supervision loop wakes on it.** `fm-watch.sh` already
   scans `state/*.status`, `fm-classify-lib.sh` already classifies the verb as
   actionable, and firstmate resumes. Zero new watcher, zero new signal.

That is the entire completion-signal mechanism: a meta registration firstmate
writes at launch + the env-pointed `parlay status` verb + firstmate's untouched
loop. It ships as part of slice 1 (the verb) plus a small firstmate-side spawn
wrapper — **not** a deferred slice-3 watcher.

- **The parlay-native supervise primitive is the END-STATE, not optional** (decision-3ae,
  revised from the earlier "optional"): parlay owns the supervisor so *every* launcher
  — not just firstmate — inherits completion-awareness, which is the general fix for
  the completion-blindness this session hit. The bridge above is what ships first; the
  primitive is Slice 3. Once it exists, firstmate's `fm-watch` wraps it with escalation
  policy instead of owning the loop.

#### 3.6.2 Unattended / away-mode supervision — **ADAPT (mechanism → Slice 3 primitive; policy → firstmate)**
§3.6.1 covers supervision **while a supervisor session is live** — a wake wakes
*someone who is watching*. Firstmate additionally owns the opposite case: the
captain is **away** and *no supervisor session exists*. Its `/afk` skill
(`AGENTS.md` §8) plus `bin/fm-afk-launch.sh` / `fm-afk-start.sh` /
`fm-supervise-daemon.sh` provide **unattended sub-supervision** — while the
durable flag `state/.afk` exists, a presence-gated daemon *owns the watcher*,
self-handles the routine majority in bash (zero LLM turns), and buffers only
captain-relevant events (`done`/`needs-decision`/`blocked`/`failed`/persistent-
wedge/check-output) as **batched, distilled digests**. This capability had **no
home** in the fold before this section: it is not §3.6.1 (which assumes a live
supervisor), and it is not the Slice 3 wake loop as first written (wake-*whom*?
nobody is present). Resolved per decision-3ae's mechanism/policy split:

**MECHANISM → the Slice 3 supervise primitive gains an UNATTENDED (headless)
mode.** The same `parlay supervise` primitive can run as a daemon with **no
supervisor session attached**, and because it is a parlay-native primitive
*every* launcher inherits unattended supervision — this is exactly the
"supervise a panel agent without firstmate present" case flagged in Q1. Ported
mechanics (from `fm-supervise-daemon.sh`, verbatim in behavior):
- **Presence gate.** A durable flag (firstmate's `state/.afk`; parlay's sink is
  env-configurable exactly as `$PARLAY_STATUS_FILE` is, §3.6) turns the daemon
  on/off. While set, the watcher reverts to daemon-owned one-shot behavior and
  **enqueues every wake to a durable queue BEFORE advancing suppression
  markers**, so a crash/restart/missed injection is recovered on the next drain
  — nothing is lost in away mode.
- **Self-handle + batch.** Routine wakes are absorbed in code; only captain-
  relevant events escalate, coalesced into one pre-read digest per batch window
  (`ESCALATE_BATCH_SECS`). **Fail-safe-to-escalate:** any wake the classifier
  cannot confidently mark routine is escalated.
- **Max-defer bound.** A buffered digest may sit at most `MAX_DEFER_SECS` before
  the daemon forces active delivery (firstmate's `.subsuper-inject-wedged`
  alarm) — bounded escalation latency, not indefinite silence.
- **In-band captain-return sentinel.** Every daemon injection is prefixed with a
  marker byte (firstmate's `FM_INJECT_MARK`, ASCII unit-separator `0x1f`, which a
  human never types). A message **with** the marker is an internal escalation
  (stay unattended); a message **without** it means the human is back → exit
  unattended, flush the "while you were out" catch-up, resume per-wake
  responsiveness. This is how one shared input channel serves both the daemon and
  the returning human.

**POLICY → firstmate retains away-mode judgment.** `fm-afk-*` shrink to thin
wrappers that put the parlay primitive into unattended mode with policy params:
- the **`/afk` enter/exit gesture** (a human decision, not a mechanic);
- the **`MAX_DEFER_SECS` value** and **what counts as captain-relevant** (the
  escalation classifier's policy inputs — the *classifying loop* is mechanism,
  the *thresholds/relevance* are policy);
- **approval-authority preservation (the load-bearing invariant):** an
  unattended daemon is a **triage/notification engine, never an approver**. It
  batches and waits; it **must not** approve a merge or any destructive/
  irreversible action on the absent captain's behalf. This mirrors the fold's
  standing rule that "yolo never authorizes destructive/irreversible without
  asking" (Q5) — away-mode makes it non-negotiable, because the human who would
  approve is definitionally not present.

Sequencing: this rides **Slice 3** (it is a mode of the same supervise
primitive, not a separate build). The near-term §3.6.1 bridge already gives
firstmate-launched agents unattended coverage for free (firstmate's existing
`/afk` daemon watches the injected `state/<id>.status`); the parlay-native
unattended mode is what lets a **non-firstmate** launcher get the same behavior
once Slice 3 lands. Until then it is DESIGNED, not built — same status as the
rest of the Slice 3 primitive.

### 3.7 Safe teardown — **ADAPT** (slice 2, paired with worktree)
Parlay has no teardown — agents self-restart (`context-reset`) or are reaped
(`--reap-ephemeral`). Neither checks for unlanded work, because parlay agents
haven't had worktrees to strand work in. Once `--worktree` exists (3.3), so does
that risk. Fold: add **`parlay-teardown <id> [--force]`** that ports firstmate's
containment gate:
- if the agent ran in a worktree, run the **same git checks**: uncommitted
  (`git status --porcelain`), unpushed (`git log HEAD --not --remotes`), and
  landed-content containment (PR-merged patch-id / `merge-tree` tree-OID equality).
  *As shipped, only the `merge-tree` tree-OID half exists — the patch-id half was
  never implemented in either CLI (robots-ceon); firstmate's `fm-teardown.sh`
  still has it if this is ever folded the rest of the way.*
  **Refuse** (`exit 1`, preserve everything) on unlanded work; `--force` is the
  only override. No auto-stash.
- close the herdr tab + kill the session (parlay already knows how — mirror
  `context-reset`'s tab reconciliation).
- retire the agent store, honoring the ephemeral marker (an adopted/named agent's
  store may be kept; an ephemeral's is removed — this is the existing
  `--reap-ephemeral` policy, now folded into an explicit teardown path).
- **KEEP `context-reset`** — teardown (destroy) and context-reset (self-restart)
  are different verbs for different intents; don't merge them.

### 3.8 Secondmates / batch / scout-report / project registry — **DROP (mostly)**
- **secondmate** (persistent domain supervisor with its own home) — DROP; parlay
  agents are already persistent peers via identity, so the secondmate abstraction
  is redundant here.
- **batch `id=repo` dispatch** — DROP for v1; parlay-spawn is one-agent-at-a-time.
  (Could return later as a thin loop.)
- **scout `report.md` deliverable** — subsumed by the `report` delivery mode
  (3.5), which uses `reply` instead of a file. KEEP the *idea*, DROP the file
  mechanics.
- **`data/projects.md` registry** — DROP the *markdown-file* form, but the concept
  is **KEPT** and backed differently: parlay's canonical project registry is the
  **`projects` bead store** (139 beads), and a spawned agent OWNS and MAINTAINS its
  bead. See §3.9. Delivery-mode-per-project is still DROPPED — mode is per-spawn
  (`--mode`), not per project.

### 3.9 Project-bead ownership — **ADAPT** (Addendum 2; slice 1)
Firstmate resolves "which project" at intake and tracks it in a markdown registry
+ tasks-axi backlog. Parlay's equivalent registry is the **`projects` bead store**
(`~/.local/bin/projects`), where **the beads ARE the live projects** — repo path in
the description (`Repo: ~/code/<x>`), and/or a `project:<slug>` label. Trillium's
requirement: a spawned/panel agent should **resolve its own project bead and keep
it live** as it works, so the canonical store never goes stale.

**The resolution problem (found while grounding):** the store is fuzzy — there are
three parlay beads today (`project-20m`, `project-8am`, `project-fiy`), some
repo-shaped, some session/episode-shaped, and `project:<slug>` labels are not
uniformly applied. An agent left to self-resolve from its cwd would guess wrong.
**So resolution happens at SPAWN time and the bead id is carried in the prompt** —
the agent is *told* which record it owns, never made to guess among namesakes.

Design (three parts):
1. **Resolve at spawn.** `parlay-spawn` gains **`--project <bead-id>`** (authoritative
   when given). When omitted, parlay-spawn attempts resolution by **reusing
   `project-match --json`** (the existing free-text → `{bead id, owning agent}`
   resolver named in the addendum) against the `--cwd`/repo; on a single confident
   match it records it, on ambiguity/none it records nothing and the brief tells the
   agent to resolve-or-ask (mirrors firstmate's "one confident match: proceed;
   ambiguous: ask one line"). *Caveat (open Q13):* `project-match` is a PAI/firstmate
   tool (`project-match.ts`), not verified on a spawned agent's PATH; the design must
   name a **parlay-native fallback resolver** — match `Repo: <cwd>` in bead
   descriptions, then `project:<slug>` label, then title substring — so resolution
   works without the external tool.
2. **Record + carry.** The resolved id lands in the identity frontmatter as
   **`project_bead=<id>`** (distinct from `project=<repo path>`, §3.2) AND is
   interpolated into the startup prompt, so a relaunch restores bead ownership too.
3. **Load + maintain.** The canonical launch contract gains a **third startup step**
   (§8.1): after `identity` + `parlay monitor --agent`, run `projects show <bead>`
   to load "what needs to happen," then **update the bead on meaningful milestones**
   — the *same* transitions that drive `parlay status` (§3.6). One milestone, two
   sinks: `parlay status` (ephemeral supervision signal) + the `projects` bead
   (durable canonical registry, via `projects comment <id>` / `projects update <id>
   --status …`). *Recommendation (open Q14):* the agent **comments/updates** its
   bead but does **not auto-close** it — closing a canonical project record is a
   supervisor/human call (consistent with "irreversible still asks"); on `done` the
   agent posts an outcome comment and lets the human/supervisor close.

---

## 4. Reconciling the two status/monitor models

This is the design's crux; stating it precisely:

```
                 parlay Monitor (KEEP)              firstmate status (ADD)
   direction     captain ──────────▶ agent          agent ──────────▶ supervisor
   transport     relay tail -F, CHAT_MSG lines       append to $PARLAY_STATUS_FILE
   payload       free-form captain messages          keyed verb: `<verb> [key=…]: <note>`
   who reads     the agent (arms its own Monitor)     firstmate's EXISTING fm-watch loop
   parlay today  ✅ exists                            ❌ unimplemented — small, not a new backend
```

**Near-term completion signal reuses firstmate's mechanism wholesale (§3.6.1):**
when firstmate launches the agent it writes a `state/<id>.meta`, points
`$PARLAY_STATUS_FILE` at `state/<id>.status`, and its already-running `fm-watch.sh`
+ `fm-classify-lib.sh` supervision wakes on the appended keyed verbs. Nothing new is
built on the read side — the "supervisor" is firstmate's existing loop, not a
parlay watcher.

They are **not two implementations of one thing**; they are the two halves of a
duplex link. A "both brains" agent:
1. **arms the parlay Monitor** to *hear* the captain (unchanged), and
2. **emits keyed status** so a supervisor can *watch* it without a human reading
   every prose reply.

Parlay's `reply` and firstmate's `.status` are two renderings of the same event
for two audiences — prose for humans, keyed states for machines. The fold's job is
to add the machine rendering, not replace the human one. The recommended unified
`parlay status` verb (3.6) makes emitting both a single agent action.

The **authoritative-current-state** distinction firstmate is emphatic about
(`.status` is an append-only *event log*, `fm-crew-state.sh` is the *current
state*) is **already handled by firstmate** for the near-term path — its
`fm-crew-state.sh` reconciles the log against run-step/pane liveness today, so a
firstmate-launched panel agent inherits that for free. The distinction only becomes
*parlay's* problem if a parlay-native watcher is ever built (optional/future).
Design note for then: parlay's current-state oracle is richer than firstmate's — parlay already knows
whether the herdr tab is live (`herdr agent get`) and whether the agent is
enrolled (`parlay subscribers`). A parlay `crew-state` reader should reconcile the
last `.status` line against **tab liveness + subscriber presence**, exactly as
`fm-crew-state.sh` reconciles against run-step + pane liveness.

---

## 5. Merged CLI / UX

```
parlay-spawn <id> <name> <color> <prompt> \
    [--cwd PATH] [--focus] [--model MODEL] \
    [--effort low|medium|high|xhigh|max] \   # NEW  (3.4)
    [--mode report|branch|pr] \              # NEW  (3.5) default: report
    [--worktree] \                           # NEW  (3.3) default: off; branch/pr imply it
    [--project <bead-id>] \                  # NEW  (3.9) canonical projects bead the agent owns;
                                             #      omitted → resolve via project-match/fallback
    [--brief-file PATH]                      # NEW  optional: task contract from a file, not inline

parlay-spawn --ephemeral <prompt> [ …same tail flags… ]

parlay status <verb> [--key <slug>] "<note>"   # NEW surface (3.6); sink = $PARLAY_STATUS_FILE
parlay status                                   #   (default ~/.parlay/agents/<id>/status), keyed
                                                #   off PARLAY_AGENT_ID. read the whole log w/ no args

parlay-teardown <id> [--force]                  # NEW (3.7) safe destroy; refuses unlanded work
```

Unchanged and still central: `reply`, `scratchpad`, `identity`, `handoff`,
`parlay monitor`, `context-reset`, `identity --launch/--mint-ephemeral/--rename/--reap-ephemeral`.

**Design invariants to preserve** (from parlay's own hard-won lessons):
- env-scrub before launching `claude` (the CLAUDECODE nesting guard).
- duplicate-tab guard + rollback-on-failure (the two-same-name-tabs bug).
- one canonical home per agent (`~/.parlay/agents/<id>/`) — extend it, never fork it.
- fail-closed frontmatter/store writes (best-effort, never wedge the spawn).

---

## 6. Smallest viable first slice

**Slice 1 — "the agent reports, and remembers why it exists" (no new risk):**
1. `parlay status` thin verb → append keyed lines to `$PARLAY_STATUS_FILE`
   (default `~/.parlay/agents/<id>/status`) — mirror `reply`/`scratchpad` plumbing;
   ~1 new command file + help + test.
2. `parlay-spawn` records `kind`/`mode`/`yolo`/`effort` into the existing
   identity frontmatter, and `--effort`/`--mode` flags (default `report`).
3. `parlay-spawn` appends the **task contract** (Task / DoD / Status protocol) to
   the startup prompt, below the untouched enrollment block.
4. **Project-bead ownership (§3.9):** `--project <bead-id>` flag + resolver (reuse
   `project-match --json`, parlay-native fallback), record `project_bead=` in
   frontmatter, carry the id into the startup prompt, and add the `projects show
   <bead>` startup step + the milestone-update instruction to the brief (bead
   updates fire on the same transitions as `parlay status`, so this is one wiring).
5. **Completion signal via firstmate reuse (§3.6.1):** a small **firstmate-side
   spawn wrapper** that, when firstmate launches a panel agent, writes a
   `state/<id>.meta` in `$FM_HOME` and injects
   `PARLAY_STATUS_FILE=$FM_HOME/state/<id>.status` into the spawn. No parlay-server
   change and no new watcher — firstmate's existing `fm-watch.sh`/`fm-classify-lib.sh`
   loop wakes on the appended status. (This wrapper lives in the firstmate repo; the
   parlay side is just the env-configurable sink from item 1.)

Slice 1 is **purely additive** — no worktree, no teardown, no new watcher — so it
can't regress existing spawns. It immediately gives firstmate's *existing* loop a
machine-classifiable completion signal, and makes a relaunched agent restore its
full lifecycle context.

**Slice 2 — isolation + safe destroy:** `--worktree`, the Setup assertion, meta
`worktree=`/`project=`, and `parlay-teardown` with the landed-work gate.

**Slice 3 — the parlay-native supervise primitive (decision-3ae end-state):** a
`parlay crew-state` reader (status-line ↔ tab-liveness ↔ subscriber-presence
reconciliation, reusing parlay's richer oracle — `herdr agent get` + `parlay
subscribers`) and a `parlay supervise` wake-on-actionable-status loop, porting
`fm-watch.sh`'s absorb-when-provably-working logic into parlay. This is where
supervision becomes a **substrate capability every launcher inherits** — the general
fix for completion-blindness. The §3.6.1 firstmate-reuse bridge is retired here:
`fm-watch` shrinks to a policy wrapper (escalation judgment) over this primitive.
The primitive also grows its **unattended (headless) mode (§3.6.2)** — the same
`parlay supervise` loop run daemon-style with no supervisor session present
(presence flag + batched escalation + max-defer + in-band captain-return marker),
so a non-firstmate launcher inherits away-mode too; firstmate's `fm-afk-*` shrink
to policy wrappers (afk gesture, max-defer value, approval-authority preservation).

**Slice 4 (end-state consolidation) — firstmate becomes thin wrappers:** once the
mechanics live in parlay (spawn/status/supervise/teardown/worktree), rewrite
`fm-spawn`/`fm-watch`/`fm-teardown` as thin policy shims that call parlay primitives
and add only the firstmate-owned policy (delivery mode + merge discipline + escalation).
Deletes the duplicate mechanical stack (canonical-home / retirement rule). The MIXED
items (backlog, fleet-state, recovery, secondmates) get split here: mechanism half →
parlay, judgment half → firstmate.

**Slice 0 — canonicalize the skill + the doc-drift TEST (ships FIRST, independent
of 1–3).** Per the addendum (robots-5cz / -0w9), the smallest viable enforcement is:
1. **Relocate the pulse-agent skill into the repo** (`skills/pulse-agent/{SKILL,INJECTING}.md`)
   and add `bin/parlay-install-skill` to symlink it back into PAI (§8.6).
2. **Clean the canonical copy**: rewrite the startup section to the two-step
   contract (`identity` + `parlay monitor --agent <id>`, §8.1) and remove/mark the
   raw-curl fallbacks so the doc passes the verifier (§8.4).
3. **Ship Form 1**: `docs/agent-docs.manifest` + a pure `scanDocForRawHttp` + a
   failing-on-drift test (§8.5). CI now blocks any reintroduction of raw-curl startup.

This slice has no dependency on the lifecycle work. The `parlay verify-agent-docs`
/ `parlay doctor` subcommand (Form 2) + the doctor symlink check reuse the same
core and land alongside slice 1 (when the brief template — a new scan target — is
introduced). Sequence: **Slice 0 (canonicalize + clean + test) → Slice 1
(brief/status/meta + completion-signal BRIDGE + Form 2 CLI + doctor symlink check)
→ Slice 2 (worktree/teardown primitives) → Slice 3 (parlay-native supervise
primitive; retire the bridge) → Slice 4 (firstmate shrinks to policy wrappers;
harness primitive last).** Slices 0–2 are additive and low-risk; 3–4 are the
decision-3ae migration proper and want their own review gate before starting.

Commit after each numbered item — never one batch commit.

---

## 7. Open design questions (with my recommendation for each)

1. **[RESOLVED] Who supervises a parlay agent — firstmate, or a parlay-native
   watcher?** *Decided (Trillium, §3.6.1):* for the near-term completion signal,
   **firstmate** — reuse its existing `state/<id>.meta` + `state/<id>.status` +
   `fm-watch.sh`/`fm-classify-lib.sh` loop, injecting `PARLAY_STATUS_FILE` at spawn.
   A parlay-native watcher is optional/future, only for supervising a panel agent
   without firstmate present. Do not invent a new signal. (Original text below.)
   *Follow-on (§3.6.2):* the "without firstmate present" case is **away-mode /
   unattended supervision** — the captain is gone and no supervisor session
   exists. That is now scoped as the Slice 3 primitive's **unattended (headless)
   mode** (mechanism: presence flag + batched escalation + max-defer + in-band
   captain-return marker) with firstmate retaining the `/afk` policy and the
   approval-authority-preservation invariant. So the parlay-native watcher is
   "optional/future" for the *attended* case but the *named home* for the
   unattended one.
   *Recommendation:* slice 1 emits status and lets firstmate's existing watcher
   read it (same grammar, zero new watcher code); build a parlay-native watcher
   only in slice 3 if parlay needs to supervise without firstmate present. This
   avoids duplicating `fm-watch.sh`'s hard-won absorb-when-provably-working logic
   prematurely.

2. **One `parlay status` verb that also posts a `reply`, or two separate verbs?**
   *Recommendation:* ship two thin verbs in slice 1 (status appends; reply posts),
   then add a `--mirror` flag (or make mirroring default) once the ergonomics are
   proven. One agent action for both audiences is the north star, but don't couple
   them before the shapes settle.

3. **Extend `identity.md` frontmatter, or add a `meta.json`?**
   *Recommendation:* extend the frontmatter. Parlay's canonical-home rule and its
   existing `identity --launch` reconstitution both argue for one record, not two.
   Firstmate uses a separate `.meta` only because its launch spec lives elsewhere;
   parlay's launch spec is *already* the frontmatter, so meta belongs there.

4. **`git worktree` directly, or depend on `treehouse`?**
   *Recommendation:* `git worktree add` as the baseline (no external dep), use
   `treehouse` only if it's on PATH. Firstmate can assume treehouse; parlay
   shouldn't inherit that coupling.

5. **Does `--worktree` change parlay's YOLO launch?** A worktree agent that can
   `git push`/open PRs under `--dangerously-skip-permissions` is more consequential
   than a chat agent. *Recommendation:* keep YOLO (parlay's remote-agent premise
   needs it), but in `mode=pr` add an explicit brief rule that opening the PR is
   the terminal action and merging still escalates to the captain — mirror
   firstmate's "yolo never authorizes destructive/irreversible without asking."

6. **Should `context-reset` and `parlay-teardown` share code?** They both close
   tabs and kill sessions. *Recommendation:* share the tab-reconciliation helper,
   keep the verbs separate — restart and destroy are different intents and must
   not be confusable at the call site.

7. **Ephemeral vs. lifecycle `kind`.** Parlay's `kind` axis would now carry both
   `ephemeral`/`named` (identity nature) and `ship`/`scout` (task nature). These
   are orthogonal. *Recommendation:* keep them as two fields — `ephemeral: true`
   stays the identity marker; add a separate `kind`/`mode` for task shape — rather
   than overloading one enum.

8. **[verifier — RESOLVED] `identity`-arms-monitor gap.** (§8.1) *Decided
   (Trillium, verified):* `~/.local/bin/identity` does NOT arm the monitor, so the
   canonical startup is **two steps** — `identity` (recover self) + `parlay monitor
   --agent <id>` (arm channel). The verifier asserts both CLI calls present + raw
   HTTP absent. Teaching `identity` to also arm the monitor is a possible future
   convenience, not the contract.

9. **[verifier] Where does the scanned agent-doc list come from — glob or
   manifest?** (§8.3) *Recommendation:* a committed `docs/agent-docs.manifest` of
   globs. Explicit and reviewable beats an implicit glob that silently starts/stops
   covering files as the tree changes; a doc that should be scanned but isn't is
   the exact silent-gap failure mode we're guarding against.

10. **[verifier] How strict on documented escape-hatches?** (§8.4)
    *Recommendation:* strict-by-default (no raw API writes in agent startup docs)
    with one greppable opt-out marker for docs that are legitimately *about* the
    HTTP API. Note this means the pulse-agent skill will fail until its raw-curl
    fallbacks are cleaned in the canonical copy — the intended forcing function.

11. **[verifier — RESOLVED] The offender is now in CI's reach.** (§8.6) *Decided
    (Trillium):* move the pulse-agent skill's canonical home into the parlay repo
    (`skills/pulse-agent/`) and symlink it into PAI, so the loaded skill and the
    CI-scanned doc are one file. Supersedes the earlier "external pointer" idea;
    robots-0w9 updated with this resolution.

12. **[verifier] Symlink whole dir vs. per-file?** (§8.6) Trillium framed it
    file-level (`…/SKILL.md`). *Recommendation:* per-file symlinks for SKILL.md AND
    INJECTING.md (both must be canonical so both are CI-scanned and the `./INJECTING.md`
    link stays valid), leaving the PAI dir able to hold PAI-only siblings later. A
    whole-dir symlink is simpler but forecloses that. Minor — flag it, don't block on it.

13. **[project-bead] Is `project-match` reachable from a spawned agent, or is a
    parlay-native fallback resolver required?** (§3.9) I could not find `project-match`
    on PATH — only `project-match.ts` referenced in PAI observability, i.e. a
    PAI/firstmate tool. *Recommendation:* reuse `project-match --json` when present,
    but **specify and build a parlay-native fallback** (`Repo: <cwd>` description
    match → `project:<slug>` label → title substring) so resolution never hard-depends
    on an external tool. And resolve at spawn time, carrying `project_bead` in the
    prompt, so multiple-namesake beads (three "parlay" beads exist today) never make
    the agent guess.

14. **[project-bead] On `done`, does the agent close its project bead or just
    comment?** (§3.9) *Recommendation:* comment/update status, do NOT auto-close —
    closing a canonical project record is consequential and belongs to the
    supervisor/human (consistent with "irreversible still asks"). Revisit if
    Trillium wants YOLO agents to close their own beads.

15. **[project-bead] Bead granularity — repo-project vs. session/episode.** (§3.9)
    Today's `projects` store mixes durable repo-projects (`project-6e5 feedtack`)
    with session/episode beads (`project-fiy` "2026-07-17/18 session…"). An agent
    "owning" a session-shaped bead is odd. *Recommendation:* out of scope for the
    spawn design — but flag that the store would benefit from a convention (one
    durable bead per repo, sessions as comments/children) so `--project` resolution
    has a stable target. Note for Trillium, not a blocker.

---

## 8. Verifier: enforcing the canonical launch contract (robots-5cz)

The fold defines *how* an agent should start; this section defines the machine
that **keeps agent docs honest about it**. Design them together — the verifier is
worthless without a stated contract, and the contract rots without enforcement.

### 8.1 The canonical launch contract (what docs MUST say)
Background (robots-5cz): agents handed vague prose ("grab identity and start the
monitor") reinvented startup with **custom API writes** — raw `curl` to
`:31337/api/*`, `Bearer dev-agent-token`, ad-hoc `register-agent` POSTs,
hand-rolled monitor wiring. The intended path is the **`identity` CLI**
(`~/.local/bin/identity`, a thin wrapper over `parlay identity`, keyed off
`PARLAY_AGENT_ID`). Canonical startup should read:

> **Load your identity, then take on `<bead>`.** Your identity load also brings up
> your monitor so the captain can reach you. Reply with `reply`. Do not hand-roll
> registration or monitoring — the `identity`/`parlay-spawn` surfaces own it.

**DECIDED (Trillium, verified): startup is TWO steps, not one.** `~/.local/bin/identity`
does **not** arm the monitor — the wrapper has zero monitor/poll references; it only
recovers durable self-knowledge. So the canonical startup contract is:

> **1. `identity`** — recover who you are (identity → handoff → scratchpad).
> **2. `parlay monitor --agent <id>`** — arm your channel so the captain can reach you.
> **3. `projects show <project_bead>`** — load your canonical project record: what
>    this project is and what needs to happen next. Update it on milestones as you
>    work (§3.9).
> **Then take on the work.** Reply with `reply`. Never hand-roll registration or
> monitoring; the `identity` + `parlay monitor` + `projects` + `parlay-spawn`
> surfaces own it.

The skill's startup wording MUST reflect these two distinct steps — it must not
imply `identity` alone launches the monitor. The verifier asserts the PRESENCE of
**both** CLI calls and the ABSENCE of raw HTTP. (Teaching `identity` to also arm the
monitor is a *possible future convenience*, not the contract — resolved as two steps
now so docs match reality today. See former Q8, now closed.)

### 8.2 What the verifier checks
Over the agent-facing doc set (8.3):
1. **PRESENCE** of the canonical contract: the doc references the `identity`-based
   load-then-take-a-bead startup and `parlay-spawn` as the spawn surface (and,
   under 8.1-A, that identity load = monitor up; under 8.1-B, the explicit
   `parlay monitor --agent` step).
2. **ABSENCE** of the anti-patterns: raw `curl`/`fetch` to `:31337/api/*`,
   `Bearer <token>` / `dev-agent-token`, ad-hoc `register-agent` POSTs, and
   hand-rolled channel/monitor wiring standing in for the `identity` CLI.

Exit non-zero naming `file:line` on any violation.

### 8.3 Scope — the single hardest design decision
The anti-pattern **legitimately exists in source**: `bin/parlay-spawn` curls
`register-agent`, `packages/cli/src/commands-nickname.ts` POSTs to it,
`packages/server/src/router-messages.ts` *defines* it. Those are the spawner and
CLI — hitting the API **is their job**. A naive repo-wide scan floods false
positives and would flag the very launcher the contract points *to*.

**Therefore the verifier scans a curated set of AGENT-FACING DOCS, never source.**
Where the list comes from (open Q9) — recommendation: a small committed manifest
`docs/agent-docs.manifest` (newline globs), so the scanned set is explicit,
reviewable, and grep-proof. It scans:
- **`skills/pulse-agent/*.md`** — the canonical pulse-agent skill, now living IN
  this repo (see 8.6). This is the primary target and the one robots-5cz/-0w9 are
  about.
- `launch-templates/default.txt` and `launch-templates/claim.txt` — the startup prompts
  that agent tabs receive (externalized from `bin/parlay-spawn`; loaded via `load_template()`).
  These are agent-facing docs describing enrollment, durable memory, shutdown protocol, and task
  context (see 8.4).
- the **brief template** introduced in slice 1 (§3.1).
- any repo `AGENTS.md` / `CLAUDE.md` (none today — future-proofing).

### 8.6 Canonical-home + symlink (resolves robots-0w9; Trillium's call)
The worst offender — `~/.claude/.agents/skills/pulse-agent/SKILL.md` — used to be
*out of this repo's CI reach*. Resolution: **move the skill's canonical home INTO
the parlay repo and symlink it back into PAI**, so one file is both the loaded
skill and a CI-scanned doc. One edit hits both; drift is impossible by construction.

- **Canonical home:** `~/code/parlay/skills/pulse-agent/` (new top-level `skills/`
  dir, mirroring firstmate's convention). It holds **both** `SKILL.md` **and
  `INJECTING.md`** — the PAI dir currently has both, and `SKILL.md` links to
  `./INJECTING.md`, so INJECTING.md must travel too or it (a) breaks the link and
  (b) escapes CI. Moving both keeps the whole skill under the verifier.
- **Symlink wiring:** the PAI copies become symlinks into the repo:
  `~/.claude/.agents/skills/pulse-agent/SKILL.md      → ~/code/parlay/skills/pulse-agent/SKILL.md`
  `~/.claude/.agents/skills/pulse-agent/INJECTING.md  → ~/code/parlay/skills/pulse-agent/INJECTING.md`
  File-level symlinks (per Trillium's framing) so the PAI dir can still hold
  PAI-only siblings later. Skill loading follows symlinks transparently (same
  mechanism as firstmate's `.claude/skills → .agents/skills`), and the relative
  `./INJECTING.md` link resolves correctly through the symlinked sibling.
- **Installer:** a small idempotent step — recommend `bin/parlay-install-skill`
  (or fold into existing setup) that: `mkdir -p` the PAI dir; for each file,
  back up any existing regular file to `*.pre-parlay.bak` once, then
  `ln -sfn <repo path> <pai path>`; verify with `readlink`. Re-runnable; converts
  a legacy regular-file install to the symlink without data loss.
- **Doctor check:** `parlay doctor` gains a check asserting the PAI paths are
  symlinks pointing at the repo canonical — so if someone later replaces the
  symlink with a divergent copy, doctor (and the verifier scanning only the repo
  copy) surfaces the split.
- **PAI-side loading note:** PAI's skill index (`.agents/skills/`) loads the file
  at that path regardless of it being a symlink, so no PAI change is needed beyond
  the symlink itself.

### 8.4 Escape-hatch handling
`pulse-agent/SKILL.md` shows raw curl partly as a *documented fallback*
("Alternative: direct poll"), not only as the startup method. The verifier must
not bless "raw curl is fine if you call it an alternative." Recommendation
(open Q10): **strict by default** — no raw API writes in an agent-facing startup
doc, period — with one explicit, greppable opt-out marker
(`<!-- parlay-verify: allow-raw-http reason=… -->`) for the rare legitimate
reference (e.g. a doc *about* the HTTP API itself). Strictness is the point;
the marker keeps it from being a straitjacket. With the skill now canonical in-repo
(§8.6), its raw-curl fallbacks get cleaned in the canonical copy during Slice 0 —
that cleanup is what makes it pass, and CI keeps it passing thereafter.

### 8.5 Two forms, smallest first
- **Form 1 — the drift TEST (ship first, before slices 1–3).** A test in parlay's
  suite (`packages/cli/src/verify-agent-docs.test.ts` or a repo-root
  `tests/agent-docs.test.ts`) that reads the manifest, loads each doc (slicing the
  heredoc for `parlay-spawn`), and asserts **no raw `curl`/`fetch` to `:31337/api/*`,
  no `Bearer <token>`.** Fails CI on drift. ~1 file, no new CLI surface, lands
  independently. This is the minimum bar the addendum asks to ship early.
- **Form 2 — the CLI verifier (generalize after).** Fold the same logic into
  `parlay doctor` (add a `verify-agent-docs` check) or a dedicated
  `parlay verify-agent-docs` subcommand: same manifest + rules, plus the PRESENCE
  check and the `file:line` reporting, runnable by hand and by the test (the test
  becomes a thin wrapper that calls the shared checker so the two never diverge).
  Shared core in `packages/cli/src/verify-agent-docs.ts`; the test imports it.

`parlay doctor` already exists (`packages/cli/src/commands-doctor.ts`), so Form 2
has a natural home. Recommendation: land Form 1 as a standalone test importing a
tiny pure `scanDocForRawHttp(text)` function, then Form 2 reuses that exact
function — one rule engine, two entry points, guaranteed consistent.

## 9. What this fold explicitly does NOT do

Read through decision-3ae (§0): "does not do" here means *not in scope for these
slices*, not "never parlay's" — the harness primitive is deferred-not-dropped.

- **No multi-harness support in slices 0–3** (claude only). The harness-adapter
  primitive is decision-3ae MECHANISM, built LAST (§3.4) with the seam scaffolded now.
- No **crew-dispatch / quota-balanced harness selection** in parlay — that is
  firstmate POLICY and STAYS FIRSTMATE (§3.4a). It is retained-not-dropped and
  re-activates against the §3.4 harness milestone; parlay never owned this choice.
- **No pluggable session backend** (herdr only) — genuinely out of scope, not deferred.
- No `no-mistakes` validation pipeline (that's firstmate POLICY, stays firstmate).
- No secondmate machinery in these slices (a MIXED item, split in Slice 4).
- No batch dispatch (could return as a thin loop).
- No replacement of `reply`, `context-reset`, or the identity/handoff recovery
  chain — those are parlay's spine and stay intact.
- The **firstmate → thin-wrapper rewrite (Slice 4)** is named as the end-state but is
  gated behind its own review; slices 0–3 stand alone and don't require it to land.
