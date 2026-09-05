# A worked parlay setup

A complete, working two-agent parlay configuration you can copy. It is derived
from a real running fleet, with every machine-specific value replaced by an
obvious stand-in — see [Sanitizing](#sanitizing) for exactly what was replaced.

Run it in a throwaway sandbox first, before you copy anything into your home
directory:

```sh
examples/bootstrap-sandbox.sh
```

That copies this example into a temp directory, builds the CLI, starts the server
on a free port with `$HOME` redirected into the sandbox, exercises it, prints
PASS/FAIL per check, and deletes the sandbox. It never touches your real
`~/.parlay`, `~/exchange`, or any running parlay server. Add `--keep` to poke
around afterwards.

Your files are safe; your network is a separate question. `packages/server` binds
every interface and has no authentication, so while the sandbox runs, anyone who
can reach that port can read its history and post as any agent. The data is seeded
fixtures on a kernel-picked high port and the window is seconds — but on a network
you do not trust, that window is real. The script prints the same limits, next to
the reply path it does not cover, when it finishes.

## What this setup is

Two agents with tabs in the panel:

| Agent | id | Shape |
|---|---|---|
| **Helm** | `helm` | Long-lived, general-purpose. Stays enrolled across restarts, has accumulated self-knowledge, is on the sweep keep-list. |
| **Reviewer** | `reviewer` | Task-scoped. Bound to one ticket in one git worktree, torn down when it lands. |

They are two shapes, not two features — most fleets are some mix of the two.

## The layout

Parlay's state splits in two, and the split is the main thing to understand:

```
examples/
├── parlay-state/     →  copy to ~/.parlay/          (the CLI and agents read this)
│   ├── config.json           which server the CLI talks to
│   ├── sweep-keep            agents `parlay sweep` must never tear down
│   └── agents/<id>/          identity.md, context.json, scratchpad.md, status
├── data-dir/         →  copy to $PARLAY_DATA_DIR    (the server writes this)
│   ├── parlay-agents.json    the agent registry — who gets a tab
│   ├── parlay-settings.json  panel + voice preferences
│   └── chat-history.jsonl    the message log
├── env.example       →  the environment variables, all of them optional
└── bootstrap-sandbox.sh
```

`~/.parlay` is the CLI's own state; the server keeps its data under
`$PARLAY_DATA_DIR`, and the CLI never reads that. They meet over HTTP — with one
exception worth knowing before you split the two apart. The reply path resolves
agent context from the **server process's** own `$HOME`, at
`~/.parlay/agents/<id>/context.json` (`loadAgentContext` in
`packages/server/src/agent-context.ts`, called on every `POST /api/chat/reply`).
The id in the request is what routes; the server accepts it via three mechanisms,
checked in order:
1. an on-disk `~/.parlay/agents/<id>/context.json` under the server's own `$HOME`;
2. the server's own agent registry (`parlay-agents.json` / the in-memory agent
   map) — an id that `parlay listen` or `POST /api/chat/register-agent` enrolled
   is routable, even with no context file and a different `$HOME`;
3. the server's own designated id — `PARLAY_AGENT_ID` — but only when the
   request actually matches that exact id, never as a blanket "any value accepts
   every id" presence check.

With none of these, the id is dropped: `parlay say --agent helm` still succeeds
and still prints `said as helm`, but the message is filed on the global thread
instead of the helm tab. Give the server the same `$HOME` as the agent store
(so mechanism 1 applies) or enroll the agent over HTTP (mechanism 2), and
`--agent` routing works regardless of what else is in its environment.

Each directory has its own README with a per-file table:
[`parlay-state/README.md`](parlay-state/README.md),
[`data-dir/README.md`](data-dir/README.md).

## Installing it for real

### Start here: a scratch directory

No copy in this path writes into `~/.parlay` or your data dir. This is the same
layout `bootstrap-sandbox.sh` builds, minus the `$HOME` redirection and the
teardown — the `PARLAY_STATE_HOME` and `parlay sweep` paragraphs below the
recipe are what that difference still leaves in reach:

Start at the repo root. Step 4 runs the server in the foreground and never
returns, so it needs a terminal of its own, and a new shell inherits nothing
from step 1 — which is why step 4 sets both roots again rather than assuming
them.

```sh
# 1. Instantiate the example somewhere new
export PARLAY_REPO="$(pwd)"
export PARLAY_EXAMPLE=~/parlay-example
mkdir -p "$PARLAY_EXAMPLE"
cp -R examples/parlay-state "$PARLAY_EXAMPLE/.parlay"
cp -R examples/data-dir     "$PARLAY_EXAMPLE/data"

# 2. Edit the placeholders
${EDITOR:-vi} "$PARLAY_EXAMPLE/.parlay/agents/helm/identity.md"     # cwd: /path/to/your/project
${EDITOR:-vi} "$PARLAY_EXAMPLE/.parlay/agents/reviewer/identity.md" # cwd/worktree/project
${EDITOR:-vi} "$PARLAY_EXAMPLE/.parlay/config.json"                 # server URL, if not localhost:4242

# 3. Build the CLI into the scratch dir — nothing on your PATH is touched
mkdir -p "$PARLAY_EXAMPLE/bin"
cd "$PARLAY_REPO/tools/cli" && go build -o "$PARLAY_EXAMPLE/bin/parlay" .

# 4. SECOND TERMINAL — run the server against the scratch data dir. This blocks
#    in the foreground until you stop it. Start in the repo root again, the same
#    directory step 1 started in: a new shell has neither export, and an unset
#    PARLAY_EXAMPLE would make PAI_DIR empty rather than absent (see below).
export PARLAY_REPO="$(pwd)"
export PARLAY_EXAMPLE=~/parlay-example
cd "$PARLAY_REPO/packages/server" && PARLAY_DATA_DIR="$PARLAY_EXAMPLE/data" \
  PAI_DIR="$PARLAY_EXAMPLE/pai" bun run start

# 5. Back in the first terminal — every CLI call carries the scratch roots, and
#    the binary is invoked by path so it cannot collide with a parlay on PATH
export PARLAY_STATE_HOME="$PARLAY_EXAMPLE/.parlay"
export PARLAY_AGENT_HOME="$PARLAY_EXAMPLE/.parlay/agents"
"$PARLAY_EXAMPLE/bin/parlay" agents
"$PARLAY_EXAMPLE/bin/parlay" send --helm "hello"
```

Every `parlay` in the prose below means that scratch binary, up to the merge
section — that one is about your real setup, so the `parlay` there is your own.
Nothing here installs onto your `PATH`: if you already have a `parlay` there,
building over it would replace it silently — and in a clone of this repo that
name is usually a symlink to the repo's own `bin/parlay` wrapper, which does more
than the bare Go binary does. `bootstrap-sandbox.sh` builds into its sandbox and
invokes it by absolute path for the same reason.

`/path/to/your/project` is the only value you *must* change. Everything else has
a working default. The two `README.md` files copied along the way are
documentation; nothing reads them.

`PAI_DIR` in step 4 is not optional decoration. `PARLAY_DATA_DIR` does not cover
it: the hook tailer, the tool tailer, and the boot-time session-channel backfill
all read `$PAI_DIR/MEMORY/OBSERVABILITY` unconditionally, and `src/tts.ts` writes
`$PAI_DIR/MEMORY/{OBSERVABILITY/tts-pronunciation-reports.jsonl,STATE/tts-cache/}`
regardless. Leave it unset and the scratch server tails your real
`~/.claude/PAI` activity and pushes it *out* of the sandbox — the tailers post
to `PARLAY_HUB_URL` (default `http://127.0.0.1:4242`), so on a box already
running a Go server there, your live agent turns land in *its* history, not the
example's (see `packages/server/README.md`).

Set it to a real path or leave it out entirely — never let it expand to empty.
The server resolves it with `??`, not `||`, so `PAI_DIR=""` is a *value*, not
"unset": `$PAI_DIR/MEMORY/…` becomes a relative path against the server's
working directory, and the tailers read — and `src/tts.ts` writes — a `MEMORY/`
tree inside `packages/server` in your clone of this repo. That is what an
unexported `$PARLAY_EXAMPLE` in step 4 would do, which is why step 4 sets it
rather than inheriting it.

`PARLAY_STATE_HOME` / `PARLAY_AGENT_HOME` cover `identity`, `scratchpad`, `say`,
`status`, and `doctor`. They do **not** cover `launch`, `teardown`, `variant`, or
`guard`, which resolve `~/.parlay/agents` from `$HOME` directly and will read
your real store even with both variables set.

The server has a `$HOME` of its own here, and step 4 leaves it at your real one.
That is the exception from [The layout](#the-layout) showing up in this recipe:
`"$PARLAY_EXAMPLE/bin/parlay" say --agent helm "…"` posts to
`/api/chat/reply`, and the server looks for `~/.parlay/agents/helm/context.json`
under *your* home rather than the scratch copy. Whether it accepts the id anyway
depends on the agent registry and the server env: an id enrolled over HTTP (a
`parlay listen --agent helm`) is routable via the server's own
`parlay-agents.json`, and the server's designated `PARLAY_AGENT_ID` routes only
for that exact id. With no context file, no registry row, and no matching env id,
it drops the channel and files the message on the global thread, printing
`said as helm` either way. To exercise `--agent` routing without leaning on those,
give the server the same home as the agent store by adding `HOME="$PARLAY_EXAMPLE"`
to step 4's command, which is the redirection `bootstrap-sandbox.sh` does.
Copying the example into your real `~/.parlay` is
not the remedy; that path overwrites live state and has its own section below.
`send` is unaffected — `/api/chat/send` takes its channel from the flag.

`parlay sweep` is the one to watch, because it is split down the middle and
`sweep --apply` is what deletes agent stores and removes worktrees. Its
**candidate list** comes from the `$HOME`-based `~/.parlay/agents` and ignores
`PARLAY_AGENT_HOME`; its **keep-list** comes from
`$PARLAY_STATE_HOME/sweep-keep`, and each candidate's status from
`PARLAY_AGENT_HOME`. Run it from the scratch setup and it enumerates your *real*
agents against the *example's* keep-list. It fails toward holding — a real agent
whose status is unreadable from the scratch store reads back as unknown, and
sweep leaves unknown alone — but do not lean on that.

`bootstrap-sandbox.sh` redirects `$HOME` as well as both variables, which is the
only way to isolate all of these completely.

**One repo-specific trap:** the `bin/parlay` wrapper at the repo root exports a
`PARLAY_SERVER` of its own, which outranks `config.json` — so `bin/parlay`
ignores the file this example ships. Build the binary directly (step 3 above)
and `config.json` decides. Full precedence and rationale:
[`parlay-state/README.md`](parlay-state/README.md#configjson).

### The server binds every interface, and has no authentication

`packages/server/src/index.ts` calls `serve({ port })` with no hostname, and
Bun's default bind address is `0.0.0.0` — so the command above listens on every
interface of the machine, and the chat API has no authentication of any kind.
Anyone who can reach the port can read your history and post as any agent. Do
not run it on a network you do not trust: firewall the port, or put something
that authenticates in front of it.

`packages/go-server` differs in one half only. It defaults to `127.0.0.1:4242`
and binds exactly that unless `-addr` / `PARLAY_SERVER_ADDR` says otherwise, so
it is loopback-only out of the box. It has no authentication either — and unlike
`packages/server`, no equivalent of `src/guard.ts`, so its write routes have no
Origin or content-type check at all. Point `-addr` at a non-loopback address and
you have the TypeScript server's exposure with less in front of it.

### Optional: merging into a real `~/.parlay`

Only once you have run the example and want to keep it. `~/.parlay` is live
state — your CLI, your agents, and `parlay sweep` all read it.

**Back it up first:**

```sh
cp -R ~/.parlay ~/.parlay.bak
```

Then copy the pieces individually. Never recursively over the whole directory:

```sh
mkdir -p ~/.parlay/agents ~/.parlay/data
cp -R examples/parlay-state/agents/helm     ~/.parlay/agents/   # OVERWRITES an existing agent store with the id "helm"
cp -R examples/parlay-state/agents/reviewer ~/.parlay/agents/   # OVERWRITES an existing agent store with the id "reviewer"
cp examples/parlay-state/config.json        ~/.parlay/          # REPLACES your persisted server URL
cp examples/parlay-state/sweep-keep         ~/.parlay/          # REPLACES your sweep keep-list: agents you had protected become sweep-eligible
# cp examples/data-dir/*.json examples/data-dir/*.jsonl ~/.parlay/data/
# ^ ONLY on a fresh data dir. This REPLACES your whole agent registry and your
#   whole message log. Uncomment it only after reading the notes below.
```

Every line above is marked because it can overwrite something of yours:

- **`config.json`** — your persisted server URL, the one `parlay remote set`
  wrote. The example ships `http://localhost:4242`. If your server is anywhere
  else, skip the file, or run `parlay remote set <your-url>` afterwards.
- **`sweep-keep`** — your keep-list. Overwriting it drops every id you had
  listed, which makes those long-lived agents sweep-eligible: `parlay sweep
  --apply` tears down any of them sitting in a terminal state. Paste the
  example's entries into your existing file by hand instead of replacing it.
- **`agents/helm`, `agents/reviewer`** — if you already run agents under those
  ids, the copy overwrites their `identity.md`, `context.json`, `scratchpad.md`,
  and `status`. Rename the example's directories first, and the `id` inside each
  `identity.md` and `context.json` with them.
- **The `data-dir/` copy** — the most destructive line in the block, because
  `env.example` documents `$HOME/.parlay/data` as the value to use and the
  `mkdir` above puts it there, so if you already run a server that way this path
  already holds live server state.
  `parlay-agents.json` is your **whole registry** — the server loads that file as
  the entire agent map, so replacing it with the example's two entries removes
  every other tab. `chat-history.jsonl` is your **whole message log**, replaced by
  four seeded lines. `parlay-settings.json` is your panel and voice preferences.
  If you already have a data dir, **skip this line entirely**, or copy only
  `parlay-settings.json`.

Then edit the placeholders in their new home and start the server. Do not reuse
steps 2-5: those are written against `$PARLAY_EXAMPLE` and would send you back to
the scratch copy. This path has its own commands, run from the repo root:

```sh
# Edit the placeholders, now under ~/.parlay
${EDITOR:-vi} ~/.parlay/agents/helm/identity.md      # cwd: /path/to/your/project
${EDITOR:-vi} ~/.parlay/agents/reviewer/identity.md  # cwd/worktree/project

# Run the server against the merged data dir
cd packages/server && PARLAY_DATA_DIR=~/.parlay/data bun run start
```

No `PARLAY_STATE_HOME` / `PARLAY_AGENT_HOME` here — `~/.parlay` is already where
the CLI looks — and no scratch binary either: the CLI on this path is whatever
`parlay` you already had, since this merges into the store it already reads.

No `PAI_DIR` either. Leaving it **unset** is correct here: it falls back to
`~/.claude/PAI`, which is the activity you now want tailed. Leave it out of the
command entirely rather than writing `PAI_DIR=`, which is not the same thing —
see the `PAI_DIR` paragraph above for what an empty value does.

## Required vs. taste

**Required for anything to work:**

- An agent's id is the same string in three places — the directory name under
  `agents/`, `context.json`'s `id`, and `identity.md`'s frontmatter `id`. Anything
  else and replies land on the wrong tab or nowhere.
- `identity.md` frontmatter `id`, `name`, and `color` — all three. `name` and
  `color` are not cosmetics: `knownAgents()` skips any agent store missing any
  one of the three, so an agent with an `id` and a `cwd` but no `color` never
  appears in `parlay launch`, and `parlay launch <id>` exits 2 with "no known
  agent" for a store that plainly exists on disk.
- A registry entry per agent — though you can skip seeding `parlay-agents.json`
  entirely and let `parlay listen` register agents on first contact.
- `cwd` in the frontmatter, naming the directory the agent belongs in. Leaving it
  out does not stop `parlay launch <id>`: `knownAgents()` substitutes your home
  directory, the agent still lists, and the spawn still goes ahead — and both
  spawners start the agent with `--dangerously-skip-permissions`. A missing `cwd`
  therefore gets you an autonomous agent running unattended in your home
  directory with permission prompts disabled, announced as a successful launch.
- `worktree` in the frontmatter for any agent that has one. `parlay teardown`
  refuses to destroy an agent whose recorded worktree holds uncommitted or
  unpushed work — and an agent with a worktree but no `worktree:` key gets no
  such check.

**Taste — this fleet's conventions, not parlay's:**

- The two-agent split itself. Nothing in the code knows about "long-lived" versus
  "task-scoped".
- `mode`, `effort`, `yolo`, `kind` in the frontmatter. The CLI records and echoes
  them; it never interprets them. This fleet's spawner does.
- Agent names, colours, and the dated `PURPOSE:` / `LESSON:` prose style in
  `identity.md`. The convention that pays for itself is *dated, one fact per line*
  — an agent recovering from a context reset reads this top to bottom.
- Everything in `parlay-settings.json`. Every key has a default; the file is
  optional. The voice phrases especially are one person's speech habits.
- Putting `$PARLAY_DATA_DIR` under `~/.parlay/data`. Any directory works. Unset,
  the server scatters those files to `~/exchange` and `$PAI_DIR/MEMORY/STATE`
  instead. Set it on a *new* instance, not on one already running without it:
  it moves the read side too, so the panel comes up empty, every tab is gone
  until each agent re-registers, and the server starts a second registry
  alongside the one you still have. Move the existing files in first, server
  stopped.

**One naming rule that is not taste:** the server's cleanup sweep deletes any
channel whose id looks like a leaked test fixture, on sight, at every sweep
including startup and however active the channel is. `-test` and `-probe` are the
ones that bite, because they are ordinary names — `api-test` and `db-probe` are
both deleted. The full pattern table is in
[`data-dir/README.md`](data-dir/README.md#parlay-agentsjson), next to the file
where you name agents; `TEST_NAME_PATTERNS` in
`packages/server/src/prune/policy.ts` is the authoritative list.

## What was verified

`bootstrap-sandbox.sh` was run against this exact directory on macOS with
`bun` + `go`, and the following passed:

- `packages/server` starts with `PARLAY_DATA_DIR` pointed at `data-dir/`'s
  contents, and its writes land only there: `chat-history.jsonl` grows past its
  seeded lines and a `PUT /api/chat/parlay/settings` shows up in
  `parlay-settings.json`, while nothing appears at the `~/exchange` or
  `$PAI_DIR/MEMORY/STATE` locations it falls back to when the variable is not
  honored.
- The seeded registry is served: `parlay agents` lists both agents.
- `parlay send --helm "…"` round-trips — read back by `parlay history` and
  appended to the sandbox's `chat-history.jsonl`.
- The seeded `chat-history.jsonl` lines load and are served back on the channel
  each one names — `parlay history --full` shows the seeded `helm` and `reviewer`
  ids with their channels intact.
- `parlay remote` resolves the server URL from the sandbox's `config.json`
  (source: `config`), with `PARLAY_SERVER` unset.
- `parlay identity --agent helm` reads `identity.md` back with the launch-spec
  frontmatter stripped.
- `parlay launch` (no args) discovers both agents' launch specs and reports them
  `[ghost]` — registered with no listener process, which is the truthful state
  for a sandbox that never arms one (liveness is registry ∩ process table).
- `parlay doctor` with `PARLAY_AGENT_ID=helm` reports PASS on identity, registry
  membership, and server reachability. Its output is captured and those three
  lines are asserted; the WARNs about the monitor and the eval engine are
  expected and not asserted. The script pins `PARLAY_EVAL_ENGINE_URL` to a dead
  port so that WARN is about the sandbox — without the pin, doctor probes the
  hardcoded `:4343` and can report PASS off a live engine the sandbox never
  started.

Every bullet above is one of the script's own PASS/FAIL checks, not something
observed by eye — if one stops holding, `bootstrap-sandbox.sh` fails.

**Not verified:**

- The browser panel. `packages/client` is served by Pulse, which is outside this
  repo; the example configures the server and CLI, and nothing here renders a tab
  in a real browser.
- `parlay listen` / `parlay monitor`, and therefore live message *delivery* to an
  agent. Both enroll through a relay daemon that is a per-runtime-dir singleton on
  the host — arming one from a sandbox is exactly the kind of cross-talk this
  example is trying to avoid. `parlay doctor` correctly WARNs "monitor not
  listening" throughout.
- `parlay launch <id>` actually spawning a process, and `parlay teardown` /
  `parlay sweep` actually collecting one. Both shell out to host tooling
  (`parlay spawn`, `herdr`) that is not part of this repo.
- `packages/go-server`, the Go rewrite of the server. It reads the same registry
  and settings shapes, but this example was exercised against `packages/server`.
- Anything on Linux or Windows. macOS only.
- `parlay doctor` also probes an eval engine at `http://127.0.0.1:4343`
  (`PARLAY_EVAL_ENGINE_URL`). Nothing in this example provides one.
  `bootstrap-sandbox.sh` pins the URL to a dead port so its doctor run WARNs
  honestly; run doctor by hand without that pin and the check reports on
  whatever happens to be listening on the machine you run it on.

## Sanitizing

This example is derived from a live personal machine. Everything below was
replaced or dropped:

**Replaced with stand-ins — change these to your own:**

- Agent ids, names, and colours. `helm` and `reviewer` are inventions; the real
  fleet's ids are its own.
- Every filesystem path is either `/path/to/your/project…` or an ordinary
  `~/.parlay` / `~/exchange`. No real home-directory layout appears.
- Server URLs are `localhost`. No hostname, tailnet name, tailnet address, or IP
  from the source machine appears anywhere.
- The `identity.md` prose. The facts shown are written for this example; they are
  the *shape* of real ones, not the content.
- The seeded `chat-history.jsonl` messages, and their ids (`00000000-…-0001`
  rather than real UUIDs).
- The voice phrases in `parlay-settings.json`. The real ones are one person's
  speech habits.
- `task: EXAMPLE-1` — a placeholder for a real ticket id.

**Deliberately omitted:**

- **Credentials of every kind.** No token, key, or secret appears here in any
  form, including redacted placeholders shaped like a real value. Parlay's config
  surface has no credential field, so there was nothing to redact — the chat API
  currently has **no authentication at all**, and the server binds every
  interface. See [the warning above](#the-server-binds-every-interface-and-has-no-authentication);
  keeping it off untrusted networks is your job, not the config's.
- The live agent roster. The source machine runs hundreds of agents; two
  representative ones are shown.
- Real `scratchpad.md` and `handoff` content — working notes about private
  projects.
- The relay, launchd, and spawner configuration (`tools/relay/deploy`,
  `tools/cli/internal/spawn`, `herdr`). Host-specific supervision, not config
  a reader copies.
- `~/.parlay/guard/`, `~/.parlay/robots-watch/`, `~/.parlay/specs/`, and
  `reincarnations.log` — runtime scratch written by daemons, not configuration.
