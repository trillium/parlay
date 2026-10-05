# A worked parlay setup

A complete, working two-agent parlay configuration you can copy. It is derived
from a real running fleet, with every machine-specific value replaced by an
obvious stand-in — see [Sanitizing](#sanitizing) for exactly what was replaced.

Run it in a throwaway sandbox first, before you copy anything into your home
directory:

```sh
examples/bootstrap-sandbox.sh
```

That copies this example into a temp directory, builds the CLI and the Go
server, starts the server on a free port with `$HOME` redirected into the
sandbox, exercises it, prints PASS/FAIL per check, and deletes the sandbox. It
never touches your real `~/.parlay` or any running parlay server, and it needs
only `go` and `curl`. Add `--keep` to poke around afterwards.

Your files are safe; your network is a separate question. The chat API has **no
authentication at all** — anyone who can reach the port can read its history and
post as any agent. The server binds `127.0.0.1` by default, so nothing off the
machine can reach it out of the box, but the sandbox is also bound to loopback
*by the address this script passes*, which is a property of the command, not a
guarantee. The data is seeded fixtures on a kernel-picked high port and the
window is seconds. The script prints the same limits, next to the delivery it
does not cover, when it finishes.

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
├── data-dir/         →  the server's -state-dir    (the server writes this)
│   ├── agents.json           the agent registry — who gets a tab
│   ├── settings.json         panel + voice preferences
│   └── messages.jsonl        the message log
├── env.example       →  the environment variables, all of them optional
└── bootstrap-sandbox.sh
```

`~/.parlay` is the CLI's own state; the server persists into whichever directory
you give it as `-state-dir` (default `~/.parlay` too), and the CLI never reads
that. They meet over HTTP, and nothing else crosses between them: the server
files a reply on whatever `agent` id the request carries
(`internal/handlers/messaging.go` in `packages/go-server`), so it never has to
look at an agent store to know where a message goes.

That means the "which `$HOME` does the server see" hazard that used to govern
`--agent` routing is gone — it was a property of the retired TypeScript server,
which resolved `<id>` against its own `~/.parlay/agents/<id>/context.json`. What
still depends on `$HOME` is the **CLI**: `launch`, `teardown`, `variant` and
`guard` resolve `~/.parlay/agents` from `$HOME` directly and ignore
`PARLAY_AGENT_HOME`. That is the redirect `bootstrap-sandbox.sh` exists to make
— and the only reason the two `$HOME`-based verb families stay safe to run
against a scratch agent store at all.

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
from step 1 — which is why step 4 sets its own variables rather than assuming
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

# 3. Build the CLI into the scratch dir — nothing on your PATH is touched.
#    CGO_ENABLED=0 because the beads dependency's embedded-Dolt tree drags in
#    ICU C++ headers under default CGO on macOS; bin/parlay sets it too.
mkdir -p "$PARLAY_EXAMPLE/bin"
(cd "$PARLAY_REPO/tools/cli" && CGO_ENABLED=0 go build -o "$PARLAY_EXAMPLE/bin/parlay" .)

# 4. SECOND TERMINAL — run the server against the scratch state dir. This
#    blocks in the foreground until you stop it. Start in the repo root again:
#    a new shell has neither export, and each `go` command must be run from
#    inside the module it names (this repo has four Go modules and no
#    root go.work — see the top-level README).
export PARLAY_REPO="$(pwd)"
export PARLAY_EXAMPLE=~/parlay-example
cd "$PARLAY_REPO/packages/go-server" && go run ./cmd/parlay-server \
  -addr 127.0.0.1:4242 \
  -state-dir "$PARLAY_EXAMPLE/data" \
  -pai-dir "$PARLAY_EXAMPLE/pai" \
  -assets-dir "$PARLAY_EXAMPLE/assets"

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

`-pai-dir` in step 4 is not optional decoration. It is the only path outside
`-state-dir` the server writes: the TTS cache
(`$PAI_DIR/MEMORY/STATE/tts-cache/`), the pronunciation report
(`$PAI_DIR/MEMORY/OBSERVABILITY/tts-pronunciation-reports.jsonl`) and
`$PAI_DIR/tts-substitutions.json`. Unset it and the server writes into your real
`~/.claude/PAI` even though every other path is scratch.

`-assets-dir` points the panel bundle lookup at an empty directory, so a real
`packages/client/dist` next to your checkout is never served from a scratch
instance. There is no panel here anyway — the bundle is not shipped (see the
top-level README's Requirements section) — but an explicit empty directory is
better than a default that silently resolves to whatever is on disk.

`PARLAY_STATE_HOME` / `PARLAY_AGENT_HOME` cover `identity`, `scratchpad`, `say`,
`status` and `doctor`. They do **not** cover `launch`, `teardown`, `variant` or
`guard`, which resolve `~/.parlay/agents` from `$HOME` directly and will read
your real store even with both variables set.

`parlay sweep` is the one to watch, because the delete inside it is still split
away from the variables. Its **candidate list** and each candidate's **status**
come from `$PARLAY_AGENT_HOME`, and its **keep-list** from
`$PARLAY_STATE_HOME/sweep-keep` — but the teardown it then performs resolves
`~/.parlay/agents` from `$HOME`. Run it from the scratch setup without redirecting
`$HOME` and it can enumerate the *example's* agents, classify them on the
example's status, and then look for those stores under *your* home. It fails
toward held — it cannot find what it was told to tear down — but do not lean on
that.

`bootstrap-sandbox.sh` redirects `$HOME` as well as both variables, which is the
only way to isolate all of these completely.

Copying the example into your real `~/.parlay` is a different operation and has
its own section below; it is not the way to fix a routing problem, because there
is no routing problem left to fix.

`send` is unaffected by all of the above — `/api/chat/send` takes its channel
straight from the flag.

### The chat API has no authentication, and binds loopback by default

There is no auth on any route. Anything that can reach the port can read the
history and post as any agent. Keep it off untrusted networks: bind loopback (the
default), or put something that authenticates in front of it.

`packages/go-server` defaults `-addr` / `PARLAY_SERVER_ADDR` to
`127.0.0.1:4242`, so it is loopback-only out of the box — point it at a LAN or
tailnet address only when something in front of it authenticates.

It does have one thing the retired TypeScript server never had: an origin
boundary. Every route is served through `internal/guard`, which rejects
cross-origin requests to the guarded set — the mutating and identifier-aiming
routes — while leaving same-origin, loopback, `.local` and private-LAN origins
alone, and allowing any request with no `Origin` header at all (which is every
CLI, `curl` and hook caller). `internal/guard.GuardedPaths` is the single list
of what is guarded; a new `/api/chat/*` route is unguarded until it is added
there. That is a browser-shaped defence, not authentication: it does nothing
about another process on the machine, or a direct socket connection.

### Optional: merging into a real `~/.parlay`

Only once you have run the example and want to keep it. `~/.parlay` is live
state — your CLI, your agents, and `parlay sweep` all read it.

**Back it up first:**

```sh
cp -R ~/.parlay ~/.parlay.bak
```

Then copy the pieces individually. Never recursively over the whole directory:

```sh
mkdir -p ~/.parlay/agents
cp -R examples/parlay-state/agents/helm     ~/.parlay/agents/   # OVERWRITES an existing agent store with the id "helm"
cp -R examples/parlay-state/agents/reviewer ~/.parlay/agents/   # OVERWRITES an existing agent store with the id "reviewer"
cp examples/parlay-state/config.json        ~/.parlay/          # REPLACES your persisted server URL
cp examples/parlay-state/sweep-keep         ~/.parlay/          # REPLACES your sweep keep-list: agents you had protected become sweep-eligible
# cp examples/data-dir/*.json examples/data-dir/*.jsonl ~/.parlay/
# ^ ONLY on a fresh, empty server state dir. This REPLACES your whole agent
#   registry and your whole message log. Uncomment it only after reading the
#   notes below.
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
- **The `data-dir/` copy** — the most destructive line in the block, because it
  lands in the server's own state dir. `agents.json` is your **whole registry**
  (the server loads that file as the entire agent map, so replacing it with the
  example's two entries removes every other tab), `messages.jsonl` is your
  **whole message log**, and `settings.json` is your panel and voice
  preferences. If your server already has state, **skip this line entirely**,
  or copy only `settings.json`.

Then edit the placeholders in their new home and start the server. Do not reuse
steps 2-5: those are written against `$PARLAY_EXAMPLE` and would send you back to
the scratch copy. This path has its own commands, run from the repo root:

```sh
# Edit the placeholders, now under ~/.parlay
${EDITOR:-vi} ~/.parlay/agents/helm/identity.md      # cwd: /path/to/your/project
${EDITOR:-vi} ~/.parlay/agents/reviewer/identity.md  # cwd/worktree/project

# Run the server against the merged state dir (the default; -state-dir is here
# only to make it explicit)
cd packages/go-server && go run ./cmd/parlay-server -state-dir ~/.parlay
```

No `PARLAY_STATE_HOME` / `PARLAY_AGENT_HOME` here — `~/.parlay` is already where
the CLI looks — and no scratch binary either: the CLI on this path is whatever
`parlay` you already had, since this merges into the store it already reads.

## Required vs. taste

**Required for anything to work:**

- An agent's id is the same string in three places — the directory name under
  `agents/`, `context.json`'s `id`, and `identity.md`'s frontmatter `id`.
- `identity.md` frontmatter `id`, `name`, and `color` — all three. `name` and
  `color` are not cosmetics: `knownAgents()` skips any agent store missing any
  one of the three, so an agent with an `id` and a `cwd` but no `color` never
  appears in `parlay launch`, and `parlay launch <id>` exits 2 with "no known
  agent" for a store that plainly exists on disk.
- A registry entry per agent — though you can skip seeding `agents.json`
  entirely and let `parlay listen` register agents on first contact.
- `cwd` in the frontmatter, naming the directory the agent belongs in. Leaving it
  out does not stop `parlay launch <id>`: `knownAgents()` substitutes your home
  directory, the agent still lists, and the spawn still goes ahead — and the
  spawner starts the agent with `--dangerously-skip-permissions`. A missing `cwd`
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
- Everything in `settings.json`. Every key has a default; the file is optional.
  The voice phrases especially are one person's speech habits.
- Where the server's `-state-dir` lives. Any directory works. Putting it at
  `~/.parlay` alongside the CLI's state is tidy for a single instance and a
  collision waiting to happen for a second one, so give each its own.

**One naming rule that is not taste:** nothing deletes an agent for its name.
An earlier generation of this example documented a server-side sweep that removed
any channel whose id looked like a leaked test fixture (`-test`, `-probe`, a
`test-` prefix, and so on) at every sweep including startup — so `api-test` and
`db-probe` were both deleted out from under you. That sweep lived in the
retired TypeScript server; the Go server has no name-pattern pass at all. Agent
lifecycle is explicit: `parlay teardown <id>`, `parlay sweep --apply`, or
`parlay shutdown <id>`.

## What was verified

`bootstrap-sandbox.sh` was run against this exact directory on macOS with `go` +
`curl`, and the following passed:

- `packages/go-server` starts against a `-state-dir` holding `data-dir/`'s
  contents, and its writes land only there: `messages.jsonl` grows past its
  seeded lines and a `PUT /api/chat/parlay/settings` shows up in `settings.json`,
  while nothing appears under the `$HOME/.parlay` and `$PAI_DIR` locations a
  server ignoring its flags would use instead.
- The seeded registry is served: `parlay agents` lists both agents.
- `parlay send --helm "…"` round-trips — read back by `parlay history` and
  appended to the sandbox's `messages.jsonl`.
- The `--agent` reply path (`POST /api/chat/reply`), which `send` never touches.
  `parlay say --agent helm "…"` returns an id and the text lands on
  `channel=helm` in `history --full` — the server files a reply on the request's
  `agent` field (`internal/handlers/messaging.go`), so there is no server-side
  context resolution for a wrong `$HOME` to break.
- The seeded `messages.jsonl` lines load and are served back on the channel
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
  lines are asserted; the WARNs about the monitor, the eval engine and the
  account file are expected and not asserted. The script pins
  `PARLAY_EVAL_ENGINE_URL` to a dead port so that WARN is about the sandbox —
  without the pin, doctor probes the hardcoded `:4343` and can report PASS off a
  live engine the sandbox never started.

Every bullet above is one of the script's own PASS/FAIL checks, not something
observed by eye — if one stops holding, `bootstrap-sandbox.sh` fails.

**Not verified:**

- The browser panel. `packages/client` is not served by this repo at all; the
  example configures the server and CLI, and nothing here renders a tab in a real
  browser.
- `parlay listen` / `parlay monitor`, and therefore live message *delivery* to an
  agent. Both enroll through a relay daemon that is a per-runtime-dir singleton on
  the host — arming one from a sandbox is exactly the kind of cross-talk this
  example is trying to avoid. `parlay doctor` correctly WARNs "monitor not
  listening" throughout.
- `parlay launch <id>` actually spawning a process, and `parlay teardown` /
  `parlay sweep` actually collecting one. The spawn pipeline itself lives in
  this repo (`tools/cli/internal/spawn`, run in-process by `parlay spawn`),
  but a real launch also needs `herdr`, which does not.
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
  `~/.parlay`. No real home-directory layout appears.
- Server URLs are `localhost`. No hostname, tailnet name, tailnet address, or IP
  from the source machine appears anywhere.
- The `identity.md` prose. The facts shown are written for this example; they are
  the *shape* of real ones, not the content.
- The seeded `messages.jsonl` messages, and their ids (`00000000-…-0001`
  rather than real UUIDs).
- The voice phrases in `settings.json`. The real ones are one person's
  speech habits.
- `task: EXAMPLE-1` — a placeholder for a real ticket id.

**Deliberately omitted:**

- **Credentials of every kind.** No token, key, or secret appears here in any
  form, including redacted placeholders shaped like a real value. Parlay's config
  surface has no credential field, so there was nothing to redact — the chat API
  currently has **no authentication at all**. See
  [the warning above](#the-chat-api-has-no-authentication-and-binds-loopback-by-default);
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
