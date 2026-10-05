# `parlay-state/` — the CLI's and agents' state

This is the CLI's and the agents' own state; the server keeps its data in its own
`-state-dir` (`PARLAY_STATE_HOME`, default `~/.parlay`) rather than here — see
[`../data-dir/README.md`](../data-dir/README.md). Nothing in this directory is
read by the server: the Go server routes a message by the `agent` field on the
request, so `--agent` works without the server ever seeing an agent store.
Point `PARLAY_STATE_HOME` / `PARLAY_AGENT_HOME` at a copy of this directory, or
merge it into an existing `~/.parlay/`. If you already have a `~/.parlay`, follow
[the merge instructions](../README.md#optional-merging-into-a-real-parlay) —
`config.json`, `sweep-keep`, and the two agent directories can each overwrite
state you are using.

| Path | What it is | Change it? |
|---|---|---|
| `config.json` | Persisted default server URL. | Yes — point it at your server. |
| `sweep-keep` | Agents `parlay sweep` must never tear down. Commented inline. | Yes — list your long-lived agents. |
| `agents/<id>/identity.md` | The agent's launch spec + durable self-knowledge. | Yes — see below. |
| `agents/<id>/context.json` | `{id, name, color}` record of the agent's identity, mirrored from `identity.md`. | Yes — keep it in step with `identity.md`. |
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
deliberately exports nothing — it only resolves the repo checkout (through any
symlinks) and execs the CLI, so the three-step resolution above applies
unchanged whether you run the binary directly or through the wrapper. (It used
to force `PARLAY_SERVER` to the legacy Pulse port, which silently beat
`config.json` for every caller.) If you build the CLI yourself, build somewhere
of your own rather than onto your `PATH`: in a clone of this repo the `parlay`
on your `PATH` is usually a symlink to `bin/parlay`, and building over it
replaces that wrapper for everything else on the machine.

## `agents/<id>/`

One directory per agent, named for the agent id, under
`$PARLAY_AGENT_HOME` (default `~/.parlay/agents`). Two agents are shipped:
`helm` (long-lived, general purpose) and `reviewer` (task-scoped, bound to a git
worktree). The id must be the same string in three places: the directory name,
`context.json`'s `id`, and `identity.md`'s frontmatter `id`. Nothing in the
server reads that agreement, so a mismatch shows up as a tab and a verb
disagreeing — not as a dropped message.

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
| `cwd` | in practice, yes | **Change this** — the directory the agent is launched in. Omitting it does not block a launch; it silently redirects one. |
| `kind` | no | Free-form: `task`, `service`, … Recorded, not interpreted by the CLI. |
| `task` | no | Ticket id this agent is bound to. |
| `worktree` | for teardown | Git worktree to remove on teardown. |
| `project` | no | The repo the worktree belongs to. |
| `mode`, `effort`, `yolo` | no | Free-form profile strings this fleet's spawner reads. Recorded, not interpreted by the CLI. |
| `account` | no | ccjuggler account the agent relaunches under. Written by a spawn that passed `--account`; read back by `parlay launch <id>` and `identity --launch <id>`. Absent means no pin — the configured default applies at relaunch time instead. |

`name` and `color` are not cosmetic either: `knownAgents()`
(`tools/cli/internal/commands/launch.go`) skips any agent store whose
frontmatter is missing `id`, `name`, or `color`, so an agent lacking one of
them is absent from a bare `parlay launch` listing and `parlay launch <id>`
exits 2 with "no known agent".

`cwd` is not enforced, and that is the trap. `knownAgents()` substitutes your home
directory when the key is missing, so the store still lists and `parlay launch
<id>` still spawns — and both spawners start the agent with
`--dangerously-skip-permissions`. A missing `cwd` therefore gets you an autonomous
agent running unattended in your home directory with permission prompts disabled,
announced as a successful launch.

`worktree` is load-bearing for safety, not cosmetic: `parlay teardown` refuses to
destroy an agent whose recorded worktree has uncommitted or unpushed work. An agent
with a worktree and no `worktree:` key is torn down without that check.

Everything below the frontmatter is prose. `parlay identity '<fact>'` appends a
line; a bare `parlay identity` prints this part with the frontmatter stripped.

### `context.json`

```json
{ "id": "helm", "name": "Helm", "color": "#6366f1" }
```

The agent's identity in machine-readable form, so a tool can read the id, name
and colour without parsing frontmatter. It must agree with `identity.md` and
with the `agents.json` registry entry, or a tab shows one name while a verb acts
on another.

**The server never reads this file.** An earlier generation of this example
documented it as the reply-attribution record the server resolved against its own
`$HOME`, with three fallback mechanisms and a silent global-thread misroute when
none of them matched. That was the retired TypeScript server. The Go server
files a `POST /api/chat/reply` on whatever `agent` field the request carries
(`internal/handlers/messaging.go`), so `--agent` routing needs nothing from the
server's environment — and the whole class of "the server is looking in the wrong
`$HOME`" traps is gone with it.

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
