# `data-dir/` — the server's persisted state

Copy this directory's contents to whatever you point the server's **`-state-dir`**
at (flag) or **`PARLAY_STATE_HOME`** at (env), then start it:

```sh
cd packages/go-server && go run ./cmd/parlay-server -state-dir /path/to/data-dir
```

Every file the server persists lives flat in that one directory. `-state-dir` is
the whole story: there is no second write location to chase, no `$PAI_DIR`
registry, and nothing that relocates when you move it. Relocating it does move
the read side, though — see below.

**Default: `~/.parlay`.** That is the same directory the CLI uses for its own
state (`config.json`, `sweep-keep`, `agents/`). Both defaulting to one place is
convenient until you are running a second instance: then give the server its own
`-state-dir` and leave `PARLAY_STATE_HOME` alone.

| File | What it is | Change it? |
|---|---|---|
| `agents.json` | The **agent registry** — every agent that gets a tab in the panel. A full snapshot, rewritten atomically on every change. | Yes: one entry per agent you run. |
| `settings.json` | Panel/voice preferences, served over `/api/chat/parlay/settings`. A full snapshot, rewritten on every `PUT`. | Optional. Every key has a default. |
| `messages.jsonl` | The message log, one `ChatMessage` per line, appended. | No — the server appends here. The four seeded lines just give a new panel something to render. |

Files the server creates on demand, so they are not shipped here: `draft.json`
(the persisted composer draft), `channels.json` (channel records) and
`uploads/` (one file per uploaded attachment).

**Move it before you start a server, not after.** Relocating the state directory
is not a pure write-side change: a server that comes back up against the new path
reads an empty history and an empty registry, so the panel has nothing to render
and no tabs until every agent re-registers. Nothing is lost — it is all still in
the old directory — but nothing is found either. Stop the server, move the
files, start it again.

## `agents.json`

A JSON **array** of `AgentInfo`
(`packages/go-server/internal/store/registry.go`; the shape is specified in
[`../../docs/api-contract.md`](../../docs/api-contract.md)):

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | Channel id. Must match the agent's directory under `agents/` and its `context.json`. |
| `name` | yes | Display name on the tab. |
| `color` | yes | Tab colour, CSS hex. |
| `nicknames` | no | Voice/picker aliases. An explicitly empty array clears them; omitting the key leaves them. |
| `urls` | no | Pages this agent owns. |
| `path` | no | Filesystem paths this agent is responsible for. |
| `caps` | no | Arbitrary JSON forwarded from `parlay listen --caps`. |

You do not have to seed this file at all — `parlay listen` / `parlay monitor`
register an agent on first contact and the server writes it here. Seeding it means
the tabs exist before any agent starts.

There is **no name-pattern cleanup sweep**. An earlier generation of this example
documented a server-side sweep that deleted any channel whose id looked like a
leaked test fixture (`-test`, `-probe`, `test-` prefixes, and so on) at every
sweep including startup. That sweep lived in the retired TypeScript server; the
Go server has no such pass, so nothing renames or removes an agent on a pattern
match. Your ids are yours. (`parlay sweep` is a separate CLI verb with its own
[`sweep-keep`](../parlay-state/sweep-keep) keep-list and explicit `--apply`; it
never acts by guessing from a name.)

## `messages.jsonl`

One `ChatMessage` per line. Required keys are `id`, `role` (`"user"` | `"agent"`),
`ts` (ISO 8601), `text`; `channel` is the agent id and is what routes a message to
a tab. `role: "user"` with no `from` means the human sent it. Optional keys —
`type`, `source`, `meta`, `images`, `from` — are documented in
`docs/api-contract.md` and in the struct's own doc comment.

The log is append-only, and it is bounded twice over: the server keeps the last
5,000 messages in memory, and compacts the file down to that window once it
passes 32 MiB (`DefaultMaxMessages` / `DefaultMaxHistoryBytes` in
`internal/store/messages.go`). It does not rotate, and nothing else reads it.

The seeded ids here are obviously fake (`00000000-…-0001`). Real ones are UUIDs the
server mints.

## `settings.json`

A single `ParlaySettings` document. Every key has a server-side default
(`DefaultSettings()` in `internal/store/settings.go`), so the file is optional —
this one is here to show the shape and to seed a panel that is not blank.

One thing to know before you copy this file over a real one: `textScale` is a
**percentage**, where 100 is the default, and the client divides by 100 when it
applies the value. The server's built-in fallback for an unset document is `1`,
which is a server-side inconsistency rather than a documented unit — if you are
writing the file yourself, write `100`, not `1`.
