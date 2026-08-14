# `data-dir/` — the server's persisted state

Copy this directory's contents to whatever you point **`PARLAY_DATA_DIR`** at, then
start the server with that env var set. Every path `packages/server` writes is
resolved in `packages/server/src/paths.ts`; setting `PARLAY_DATA_DIR` relocates all
of them, flat, into that one directory.

With `PARLAY_DATA_DIR` **unset**, the same files scatter to their production
locations instead — `~/exchange/` for most of them, `$PAI_DIR/MEMORY/STATE/` for
the registry. That split is historical. Set `PARLAY_DATA_DIR`; it is one directory
you can back up, inspect, and throw away.

| File | What it is | Change it? |
|---|---|---|
| `parlay-agents.json` | The **agent registry** — every agent that gets a tab in the panel. | Yes: one entry per agent you run. |
| `parlay-settings.json` | Panel/voice preferences, served over `/api/chat/parlay-settings`. | Optional. Every key has a default. |
| `chat-history.jsonl` | The message log, one JSON object per line. Rotates at 5 MB. | No — the server appends here. The four seeded lines just give a new panel something to render. |

Files the server creates on demand, so they are not shipped here: `chat-draft.txt`
(persisted composer draft), `parlay-agent-channels.json` and
`parlay-session-channels.json` (session→channel maps), and `parlay-uploads/`
(image attachments).

## `parlay-agents.json`

A JSON **array** of `AgentInfo` (`packages/server/src/types.ts`, mirrored by
`packages/go-server/internal/store/registry.go`):

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | Channel id. Must match the agent's directory under `agents/` and its `context.json`. |
| `name` | yes | Display name on the tab. |
| `color` | yes | Tab colour, CSS hex. |
| `nicknames` | no | Voice/picker aliases. |
| `urls` | no | Pages this agent owns. |
| `path` | no | Filesystem paths this agent is responsible for. |
| `caps` | no | Arbitrary JSON forwarded from `parlay listen --caps`. |

You do not have to seed this file at all — `parlay listen` / `parlay monitor`
register an agent on first contact and the server writes it here. Seeding it means
the tabs exist before any agent starts.

**Do not name an agent `test-…`, `bench-…`, `forge-…`, `profile-…`, `busy-…`, or
anything ending in `z<digit>`.** The server's autonomous cleanup sweep
(`packages/server/src/prune/policy.ts`) deletes channels matching those patterns on
sight, at every sweep, regardless of how active they are — they are the fingerprints
of leaked test fixtures. `helm` and `reviewer` are safe; `reviewer-z1` would be
deleted out from under you.

## `chat-history.jsonl`

One `ChatMessage` per line. Required keys are `id`, `role` (`"user"` | `"agent"`),
`ts` (ISO 8601), `text`; `channel` is the agent id and is what routes a message to a
tab. `role: "user"` with no `from` means the human sent it. Optional keys —
`type`, `action`, `source`, `meta`, `images`, `from` — are documented in
`packages/server/src/types.ts`.

The seeded ids here are obviously fake (`00000000-…-0001`). Real ones are UUIDs the
server mints.
