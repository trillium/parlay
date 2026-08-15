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

| Doc | What it is |
|---|---|
| [`api-contract.md`](api-contract.md) | The HTTP/SSE contract for every `/api/chat/*` route, shared by the client, the CLI, and both server implementations. The most useful doc here if you are building against parlay. |
| [`COMMAND_DESIGN_CONTRACT.md`](COMMAND_DESIGN_CONTRACT.md) | How a voice/text command must be shaped so the Go eval engine can load it without being recompiled. |
| [`CHANNEL_PICKER_CONTRACT.md`](CHANNEL_PICKER_CONTRACT.md) | The frozen event/action wire contract between the Go eval engine and the TS panel for the voice-driven channel picker. |
| [`context-reset-single-tab.md`](context-reset-single-tab.md) | Why `bin/context-reset` is shaped the way it is — the single-tab guarantee when an agent restarts itself. |
| [`VERSIONING.md`](VERSIONING.md) | The two version axes (repo semver tag vs. panel `PA_VERSION`) and the automatic tagging scheme. |

## Internal — integration with the author's agent fleet

| Doc | What it is |
|---|---|
| [`PARLAY_FIRSTMATE_FOLD.md`](PARLAY_FIRSTMATE_FOLD.md) | A design doc (status: **design, not built**) for migrating agent-lifecycle mechanics out of [*firstmate*](https://github.com/trillium/firstmate) — the sibling agent-fleet orchestrator — and down into parlay. Assumes firstmate, PAI, herdr, and the beads stores throughout. |
| [`CLI_VERBS_AND_EVENTS.md`](CLI_VERBS_AND_EVENTS.md) | Two references in one: §1, how to author a `parlay <verb>` subcommand, has sound mechanics but describes the retired TypeScript CLI under `packages/cli/src/` — `tools/cli` is the live surface, so read it there before adding a verb. §2's event-fabric design is motivated by a private `robots` bead store firing `mechanic-dispatch`, which you will not have. |
