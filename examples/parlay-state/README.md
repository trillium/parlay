# `parlay-state/` — the CLI's and agents' state

This is the CLI's and the agents' own state; the server keeps its data under
`$PARLAY_DATA_DIR` rather than here. One exception matters in practice: the reply
path resolves agent context from the **server process's** own `$HOME`, so
`--agent` routing works only when the agent store is visible to the server — see
the [`context.json`](#contextjson) section below. Point
`PARLAY_STATE_HOME` / `PARLAY_AGENT_HOME` at a copy of this directory, or merge
it into an existing `~/.parlay/`. If you already have a `~/.parlay`, follow
[the merge instructions](../README.md#optional-merging-into-a-real-parlay) —
`config.json`, `sweep-keep`, and the two agent directories can each overwrite
state you are using.

| Path | What it is | Change it? |
|---|---|---|
| `config.json` | Persisted default server URL. | Yes — point it at your server. |
| `sweep-keep` | Agents `parlay sweep` must never tear down. Commented inline. | Yes — list your long-lived agents. |
| `agents/<id>/identity.md` | The agent's launch spec + durable self-knowledge. | Yes — see below. |
| `agents/<id>/context.json` | `{id, name, color}` reply-attribution record — the server reads it from its own `$HOME`. | Yes — must match `identity.md` and the registry. |
| `agents/<id>/scratchpad.md` | The agent's working notes. | No — the agent writes it. Created on first write. |
| `agents/<id>/status` | Append-only agent→supervisor status lines. | No — `parlay status <verb> "<line>"` appends. |

## `config.json`

```json
{ "server": "http://localhost:4242" }
```

Server URL resolution, highest wins (`tools/cli/internal/config`):

1. `PARLAY_SERVER` env var
2. this file's `server` key
3. the coded default `http://localhost:4242`

`parlay remote set <url>` writes it, `parlay remote clear` empties it, and
`parlay remote` prints which of the three is currently winning. A missing or
corrupt file is treated as empty — resolution just falls through.

**Note for readers of this repo:** the `bin/parlay` wrapper in the repo root
exports `PARLAY_SERVER=http://localhost:31337` before exec'ing the binary, because
this fleet serves parlay through Pulse on that port. That env var beats
`config.json`, so `bin/parlay` ignores the file. Build the CLI directly —
`cd tools/cli && go build -o <scratch-dir>/bin/parlay .`, then invoke it by that
path — if you want `config.json` to be the thing that decides. Build somewhere of
your own rather than onto your `PATH`: in a clone of this repo the `parlay` on
your `PATH` is usually a symlink to `bin/parlay`, and building over it replaces
that wrapper for everything else on the machine.

## `agents/<id>/`

One directory per agent, named for the agent id, under
`$PARLAY_AGENT_HOME` (default `~/.parlay/agents`). Two agents are shipped:
`helm` (long-lived, general purpose) and `reviewer` (task-scoped, bound to a git
worktree). The id must be the same string in three places: the directory name,
`context.json`'s `id`, and `identity.md`'s frontmatter `id`.

### `identity.md` frontmatter — the launch spec

Written by `parlay identity --register …`, read back by `parlay launch <id>`,
`parlay teardown`, and `parlay sweep`
(`tools/cli/internal/identity/mem.go`). Hand-editing it is fine.

| Key | Required | Meaning |
|---|---|---|
| `id` | yes | The agent id. |
| `name` | for launch + a tab | Display name. |
| `color` | for launch + a tab | Tab colour. |
| `model` | no | Model `parlay launch <id>` respawns with. |
| `cwd` | for launch | **Change this** — the directory the agent is launched in. |
| `kind` | no | Free-form: `task`, `service`, … Recorded, not interpreted by the CLI. |
| `task` | no | Ticket id this agent is bound to. |
| `worktree` | for teardown | Git worktree to remove on teardown. |
| `project` | no | The repo the worktree belongs to. |
| `mode`, `effort`, `yolo` | no | Free-form profile strings this fleet's spawner reads. Recorded, not interpreted by the CLI. |

`name` and `color` are not cosmetic either: `knownAgents()`
(`tools/cli/internal/commands/launch.go`) skips any agent store whose
frontmatter is missing `id`, `name`, or `color`, so an agent lacking one of
them is absent from a bare `parlay launch` listing and `parlay launch <id>`
exits 2 with "no known agent".

`worktree` is load-bearing for safety, not cosmetic: `parlay teardown` refuses to
destroy an agent whose recorded worktree has uncommitted or unpushed work. An agent
with a worktree and no `worktree:` key is torn down without that check.

Everything below the frontmatter is prose. `parlay identity '<fact>'` appends a
line; a bare `parlay identity` prints this part with the frontmatter stripped.

### `context.json`

```json
{ "id": "helm", "name": "Helm", "color": "#6366f1" }
```

The reply-attribution record, and the one place in this directory where whose
`$HOME` is in play changes the outcome. `loadAgentContext`
(`packages/server/src/agent-context.ts`, called on every `POST /api/chat/reply`)
resolves `~/.parlay/agents/<id>/context.json` against the **server process's**
own `$HOME` — not `PARLAY_AGENT_HOME`, and not the home of the CLI that sent the
message. So `parlay say --agent helm` reaches the helm tab only when the server
can see this file under the home *it* is running with. When it cannot, the reply
still succeeds and the CLI still prints `said as helm`, but the server drops the
channel and files the message on the global thread. The fix is to run the server
under the same `$HOME` as the agent store — not to copy this directory into a
live `~/.parlay`.

### `status`

Appended to by `parlay status <verb> "<line>"`, one line per supervisor-actionable
transition, read by `parlay crew-state <id>` and `parlay supervise <id>`. Verbs:
`working`, `needs-decision`, `blocked`, `paused`, `done`, `failed`, `resolved`
(`tools/cli/internal/commands/status_verb.go`).

`parlay sweep` reads this file to decide what it may collect, and only **`done`**
is collectable. `needs-decision`, `blocked`, and `failed` are terminal too, but
they are the ones a human still has to read, so sweep *holds* and reports them
instead of absorbing them — a failed agent stays until you deal with it. `done`
alone is not enough either: unless you name the agent explicitly, sweep also
requires the store to prove it was a per-task spawn, with a `task:` or a
`worktree:` in its `identity.md` frontmatter. Anything it cannot prove is held.
`sweep-keep` overrides all of it (`ClassifySweep` in
`tools/cli/internal/commands/sweep.go` is the whole policy).
