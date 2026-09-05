# docs/

Two kinds of document live here. The first kind is useful to anyone running parlay.
The second kind documents how parlay integrates with the author's own agent fleet.
Part of that fleet is public: [**firstmate**](https://github.com/trillium/firstmate),
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

## Generally useful

**System map** — one deep-dive per load-bearing part, linked from the root README's system map:

| Doc | What it is |
|---|---|
| [`input.md`](input.md) | The `parlay-input` DOM wrapper — the up-channel from a composer element into the phrase engine. |
| [`command-server.md`](command-server.md) | The Bun/`packages/server` and Go/`packages/go-server` chat API implementations — what each owns, and the current gap between them. |
| [`events-history.md`](events-history.md) | The append-only chat-history JSONL files and the hook/tool tailers that feed them. |
| [`agent-registry.md`](agent-registry.md) | Who is enrolled as a chat tab, and the transient presence counters — distinct from the live-command registry below. |
| [`monitor.md`](monitor.md) | `parlay monitor`/`listen` — how an agent receives messages, relay-backed or legacy-poll. |
| [`launcher.md`](launcher.md) | `parlay spawn` — the in-process Go launcher (`tools/cli/internal/spawn`) and the `bin/parlay-spawn` bash escape hatch. |
| [`relay.md`](relay.md) | The per-runtime-dir fan-out daemon between the server and every enrolled agent's monitor. |

| Doc | What it is |
|---|---|
| [`api-contract.md`](api-contract.md) | The HTTP/SSE contract for every `/api/chat/*` route, shared by the client, the CLI, and both server implementations. The most useful doc here if you are building against parlay. |
| [`COMMAND_DESIGN_CONTRACT.md`](COMMAND_DESIGN_CONTRACT.md) | How a voice/text command must be shaped so the Go eval engine can load it without being recompiled. |
| [`CHANNEL_PICKER_CONTRACT.md`](CHANNEL_PICKER_CONTRACT.md) | The frozen event/action wire contract between the Go eval engine and the TS panel for the voice-driven channel picker. |
| [`context-reset-single-tab.md`](context-reset-single-tab.md) | Why `bin/context-reset` is shaped the way it is — the single-tab guarantee when an agent restarts itself. |
| [`live-commands.md`](live-commands.md) | The live-command registry: how a running `parlay` verb reports itself, why the registry stores no free-form text (verb, agent id, pid, flag *names* only), and the 90s staleness reaper that keeps a crashed command from becoming a permanent zombie entry. |
| [`VERSIONING.md`](VERSIONING.md) | The two version axes (repo semver tag vs. panel `PA_VERSION`) and the automatic tagging scheme. |

## Internal — integration with the author's agent fleet

| Doc | What it is |
|---|---|
| [`gascity-integration-contract.md`](gascity-integration-contract.md) | The binding contract for the in-progress [Gas City](https://github.com/gastownhall/gascity) adoption epic: the pinned upstream ref, the vendored `third_party/gascity/openapi.json` and its sha256, the chosen integration mode, and the collision/irreversibility registers every later unit of the epic is bound by. Nothing here ships yet — no seam is implemented and no Gas City dependency exists. |
| [`gascity-plane-boundary.md`](gascity-plane-boundary.md) | Reads the contract above and states, capability by capability, where the Gas City ↔ parlay ownership boundary falls: what the EXECUTION plane owns, what the PROCESS + REPRESENTATION plane owns, the seam obligation parlay must meet for each split, and a register of the splits that are still open. A documentation artifact, not a contract — where the two disagree, the contract wins. |
| [`status-lift-topology.md`](status-lift-topology.md) | Decision record for the crew-status lift (epic `task-4cfpv.12`, unit 0): how parlay code reaches a beads store. Adopts direct import of `github.com/steveyegge/beads` at a parlay-controlled path over the `gc` CLI, `gc bd`, and HTTP topologies, and states the adopted option's costs (Dolt, CGO-or-server-mode). Decision only — no reader or writer is cut over by it. |
| [`crew-bead-schema.md`](crew-bead-schema.md) | The crew-status lift's normative schema (epic `task-4cfpv.12`, unit 2): what a crew bead looks like, the 7-verb → beads-status mapping, the metadata key vocabulary (including `decision.<slug>` keyed decisions), and the three-vocabulary crosswalk against Gas City lifecycle states and issue #128 §5. Machine-readable half is `tools/cli/internal/parlaybeads/schema.go`; the two move together. Nothing consumes it yet — the writer and reconciler are later units. |
| [`CLI_VERBS_AND_EVENTS.md`](CLI_VERBS_AND_EVENTS.md) | Two references in one: §1, how to author a `parlay <verb>` subcommand, has sound mechanics but describes the retired TypeScript CLI under `packages/cli/src/` — `tools/cli` is the live surface, so read it there before adding a verb. §2's event-fabric design is motivated by a private `robots` bead store, which you will not have, firing `mechanic-dispatch`. |
