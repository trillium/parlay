# docs/

**If you are here to run parlay, you want the [root README](../README.md)** — its
Quickstart takes a fresh clone to a working server-and-CLI round-trip in a handful of
commands. This file is the index for everything below that directory.

The docs sort four ways: **what you need to run parlay**, **what you need before
changing it**, **what describes a system that no longer exists**, and **what is
internal to the author's own agent fleet**. A fifth group, the dated field reports, is
evidence rather than specification.

The fleet part needs one caveat up front. Part of that fleet is public:
[**firstmate**](https://github.com/trillium/firstmate),
the agent-fleet orchestrator that drives crews of coding agents through parlay from
intake through review to merge, and [**herdr**](https://github.com/trillium/herdr),
the terminal they run in. The rest is not — **PAI**, the **beads** `bd` binary and
the `~/data/…` stores its `robots`/`task`/`projects` wrappers drive (the `robots`
wrapper itself is here, at `tools/robots-emit/robots`; the binary and the stores
behind it are not), and the private robots store `mechanic-dispatch` fires from
(that wrapper is here too, at `tools/mechanic-dispatch/`). Those docs are
written for an internal audience, and nothing in them is required to run parlay. They
are kept because the design reasoning is real and travels with the code — not because
you are expected to reproduce the setup.

**Every top-level `.md` file in this directory is listed below.** CI
([`hygiene`](https://github.com/trillium/parlay/blob/main/.github/workflows/ci.yml))
enforces it: a new doc that is not linked from this file fails the build, because an
unindexed doc is one nobody finds.

## Generally useful — if you run parlay

### Start here, in the order you will meet things

| Doc | What it is |
|---|---|
| [`traps.md`](traps.md) | The same knowledge as [`AGENTS.md`](../AGENTS.md) and `agent-notes/`, but ordered by **when it will bite you** rather than by when the incident happened: nine stages from "before you have run anything" to "only if you go deeper", every note linking its full rationale. Read the stage you are in. |

### One deep-dive per load-bearing part

Linked from the root README's system map.

| Doc | What it is |
|---|---|
| [`input.md`](input.md) | The `parlay-input` DOM wrapper — the up-channel from a composer element into the phrase engine. |
| [`command-server.md`](command-server.md) | The Go chat API server (`packages/go-server`) — what it owns, how it runs, and the wire contract |
| [`events-history.md`](events-history.md) | The chat-history file the Go server actually keeps (`messages.jsonl`), what reads it, and the out-of-process hook/tool streams that feed it. |
| [`agent-registry.md`](agent-registry.md) | Who is enrolled as a chat tab, and the transient presence counters — distinct from the live-command registry below. |
| [`monitor.md`](monitor.md) | `parlay monitor`/`listen` — how an agent receives messages, relay-backed or legacy-poll. |
| [`launcher.md`](launcher.md) | `parlay spawn` — the in-process Go launcher (`tools/cli/internal/spawn`), the only spawner. |
| [`relay.md`](relay.md) | The per-runtime-dir fan-out daemon between the server and every enrolled agent's monitor. |
| [`live-commands.md`](live-commands.md) | The live-command registry: how a running `parlay` verb reports itself, why the registry stores no free-form text (verb, agent id, pid, flag *names* only), and the 90s staleness reaper that keeps a crashed command from becoming a permanent zombie entry. |
| [`action-log.md`](action-log.md) | The **command log** — one row per *evaluated string* (not per process), its four-value outcome vocabulary (`delivered`/`queued`/`dropped`/`refused`), the filter axes, and the off switch that turns a connection or an action off from the same place you read the rows. The sandbox test site's backing surfaces. |

### Contracts — wire shapes other code is built against

| Doc | What it is |
|---|---|
| [`api-contract.md`](api-contract.md) | The HTTP/SSE contract for every `/api/chat/*` route, shared by the client, the CLI, and the Go server. The most useful doc here if you are building against parlay. Its machine-readable twin is [`api-contract.openapi.yaml`](api-contract.openapi.yaml), which is **authoritative** where the two disagree. |
| [`COMMAND_DESIGN_CONTRACT.md`](COMMAND_DESIGN_CONTRACT.md) | How a voice/text command must be shaped so the Go eval engine can load it without being recompiled. |
| [`CHANNEL_PICKER_CONTRACT.md`](CHANNEL_PICKER_CONTRACT.md) | The frozen event/action wire contract between the Go eval engine and the TS panel for the voice-driven channel picker. |
| [`context-reset-single-tab.md`](context-reset-single-tab.md) | Why `bin/context-reset` is shaped the way it is — the single-tab guarantee when an agent restarts itself. |
| [`VERSIONING.md`](VERSIONING.md) | The two version axes (repo semver tag vs. panel `PA_VERSION`) and the automatic tagging scheme. |

## Design records — if you change parlay

Not needed to run it. Each states a decision and its reasoning; the code implementing
one is named at the top of its doc.

| Doc | What it is |
|---|---|
| [`routing.md`](routing.md) | Deterministic-first routing with confidence and progressive hardening — the representation-plane routing engine (`tools/cli/internal/routing`) behind `parlay route`. Only exit 0 acts; a route decision cannot be talked into acting. |
| [`staleness-model.md`](staleness-model.md) | Record staleness (`tools/cli/internal/staleness`) — staleness of beads/work-product records, derived by comparison and never eagerly cascaded. Unrelated to `parlay stale`/`sweep`, which are about agent worktrees; the doc opens with that disambiguation. |
| [`supersession.md`](supersession.md) | The supersession policy (`tools/cli/internal/supersession`) — a superseded record is never mutated, severity is validated against a classified changeset floor, and superseding something a human already acted on is never silent. |
| [`source-contracts.md`](source-contracts.md) | Source enrollment contracts for human input surfaces: the canonical `contracts/sources/*.json` and what enrolling a surface obliges it to. |
| [`interface-capabilities.md`](interface-capabilities.md) | Interface capability declaration and delivery gating — how a surface declares (via `?caps=`) which presentation commands it can accept, so state is never routed to a surface that cannot present it. |
| [`remote-input.md`](remote-input.md) | The remote/voice input control plane: text parlay has accepted turned into real keystrokes on the target Mac, behind the `TalonAdapter` seam. |

## Gas City adoption

An in-progress epic with its own contract-first chain. Nothing here is wired into a
running parlay yet, and nothing in this section is required to run parlay.

| Doc | What it is |
|---|---|
| [`gc-prerequisite.md`](gc-prerequisite.md) | Why the Gas City `gc` binary is a documented **runtime prerequisite** — absent-or-too-old is a named error with an install pointer, never a silent degrade. Start here if you are wondering what the Gas City plane needs installed. |
| [`gascity-integration-contract.md`](gascity-integration-contract.md) | The binding contract for the adoption epic: the pinned upstream ref, the vendored `openapi.json` and its sha256, the chosen integration mode, and the collision/irreversibility registers every later unit is bound by. Nothing here ships yet. |
| [`gascity-plane-boundary.md`](gascity-plane-boundary.md) | Where the Gas City ↔ parlay ownership boundary falls, capability by capability, with the seam obligation for each split. A documentation artifact — where it and the contract disagree, the contract wins. |
| [`status-lift-topology.md`](status-lift-topology.md) | Decision record for the crew-status lift: how parlay reaches a beads store, and the costs of the adopted option. Decision only — no reader or writer is cut over by it. |
| [`crew-bead-schema.md`](crew-bead-schema.md) | The crew-status lift's normative schema — what a crew bead looks like, the verb → beads-status mapping, and the metadata key vocabulary. The machine-readable half (`schema.go`) moves with it. |

## Historical — describes a system that no longer exists

Kept because the reasoning travelled, but **do not use these to find current
behavior**. Each names the doc that replaced it.

| Doc | What it is |
|---|---|
| [`scope-go-spawn.md`](scope-go-spawn.md) | What the `bin/parlay-spawn` → Go reconciliation had to close, as a record. Superseded by [`launcher.md`](launcher.md): there is now exactly one spawner, in-process in `tools/cli/internal/spawn`. |
| [`go-cli-parity.md`](go-cli-parity.md) | The per-verb TypeScript→Go CLI migration survey. The TS CLI was deleted in T-08, so its columns describe a tree you reach with `git show 871b3f8f^:packages/cli/src/<file>`. |

## Field reports — dated evaluations, kept as evidence

Each is a point-in-time pass from a newcomer's or a user's seat, against an isolated
instance. They deliberately walk **different ground** rather than repeating each other,
so read the one that names the surface you care about; each carries its own
"unfixed findings" list, which is history rather than a live to-do list.

| Doc | What it is |
|---|---|
| [`ux-eval-2026-08-30.md`](ux-eval-2026-08-30.md) | End-to-end walk of the user journeys following only the docs, with per-journey verdicts and an unfixed-findings list. |
| [`dogfood/2026-08-31-friction-log.md`](dogfood/2026-08-31-friction-log.md) | A second pass walking different ground — capability declaration, source contracts, SSE reconnect, enrollment failure modes. |
| [`soak/2026-08-31-optin-soak-pass1.md`](soak/2026-08-31-optin-soak-pass1.md) | Hermetic evidence pass over the four opt-in Gas City surfaces, with a verdict and defect list per surface. |

## Internal — integration with the author's agent fleet

| Doc | What it is |
|---|---|
| [`CLI_VERBS_AND_EVENTS.md`](CLI_VERBS_AND_EVENTS.md) | Two references in one: §1, how to author a `parlay <verb>` subcommand, has sound mechanics but describes the retired TypeScript CLI under `packages/cli/src/` — `tools/cli` is the live surface, so read it there before adding a verb. §2's event-fabric design is motivated by a private `robots` bead store, which you will not have, firing `mechanic-dispatch`. |
| [`pi-inbox-bridge.md`](pi-inbox-bridge.md) | The fleet's inbox worker — how `inbox create` wakes a Pi pane via the `pi-inbox` channel, and the `/inbox-connect` opt-in. Part of `examples/fleet/`, not core product. |
| [`agent-notes/fleet-vs-core-split-parlay-dev.md`](agent-notes/fleet-vs-core-split-parlay-dev.md) | The fleet-vs-core split doctrine: why the inbox/skills glue lives in `examples/fleet/` (not `tools/`/`skills/`), the `parlay` vs `parlay-dev` wrapper modes, and the two symlink gotchas. Root README's "Fleet layer and `parlay-dev`" section is the user-facing summary. |
| [`upstream/gascity-ask.md`](upstream/gascity-ask.md) | A **draft, not posted** upstream issue to [gastownhall/gascity](https://github.com/gastownhall/gascity), with every claim anchored to the pinned ref. Posting it is captain-gated; nothing in it has been sent. |

## `agent-notes/`

The directory beside this file is the reverse of this index: not documents, but
**one lesson per file**, each written when something in this repo caused a real
incident on the author's machine. There is no alphabetical per-note index —
[`AGENTS.md`](../AGENTS.md) is the one that matters, and it loads into every agent
session, so read it first and follow its pointers here when one applies. Ordered by the
order a newcomer meets them, the same notes are [`traps.md`](traps.md).