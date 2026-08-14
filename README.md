# Parlay

**A tailnet-wide chat channel between you and your background AI agents.**

Parlay is a chat panel + CLI that gives long-running AI agents a per-agent
conversation channel with the human ("the captain"), durable memory that survives
context resets, and tooling for spawning and supervising agent work. It's served by
[Pulse](https://github.com/trillium) on `:31337` and reachable **across your
[Tailscale](https://tailscale.com) tailnet** — so agents running on one machine are
reachable from your phone, laptop, or any other device on the tailnet, not just
`localhost`. The panel injects into any page Pulse serves.

> Status: **alpha**, single-owner. Interfaces move fast.

---

## Why

Background agents (code assistants, watchers, task runners) need a way to reach a
human that isn't a terminal you have to be sitting in front of. Parlay gives each
agent its own chat tab, reachable from your phone, with:

- **Per-agent channels, tailnet-wide** — every enrolled agent gets a tab; messages
  route only to the agent they're addressed to, reachable from any device on your
  tailnet.
- **Durable identity + memory** — `identity`, `scratchpad`, and `handoff` persist
  across restarts, so an agent that resets its context recovers *who it is* and
  *what it was doing* via the `identity → handoff → scratchpad` chain.
- **Spawn + supervise** — launch a fresh background agent that auto-enrols in the
  panel (`parlay-spawn`), and drive event-based follow-ups.
- **Voice-first** — a compiled phrase engine turns spoken commands into panel
  actions; agent replies can be read aloud with per-passage playback.

## Layout

A [Bun](https://bun.sh) workspace monorepo:

| Package | What it is |
|---|---|
| `packages/client` | The chat panel injected into Pulse pages — tabs, presence, message rendering, TTS/speech playback, annotations. |
| `packages/server` | The Bun server (runs inside Pulse): chat history, SSE, long-poll, the per-agent relay, upload/link handling. |
| `tools/cli` | The Go rewrite of the `parlay` command surface — `reply`/`say`, `monitor`, `identity`/`scratchpad`/`handoff`, `alert`, `robots-watch`/`robots-tail`, `doctor`/`health`, and more. `bin/parlay` builds and execs this binary. |
| `packages/cli` | The original TS `parlay` command surface. Superseded by `tools/cli`; kept only for `lavish-import`, which `bin/parlay` still routes here pending a Go port. |
| `packages/eval-engine` | A compiled Go (RE2) engine that matches spoken/typed phrases to a closed set of panel actions. |
| `packages/input` | `parlay-input` — a self-contained, framework-agnostic DOM input wrapper (a real client for parlay's REST + shared-SSE input protocol) for wiring a UI input to a parlay server. The one publishable npm package; flat unscoped name, no dependencies. |

Agent-facing entry points live in `bin/` (`parlay`, `parlay-spawn`, …) and the
agent skill documenting enrolment.

## Quickstart

```sh
bun install                 # also wires the git hooks (core.hooksPath tools/hooks)
```

Parlay is served as a module of Pulse. With Pulse running, the panel is available
at `http://localhost:31337/chat-app/` on the host — or at the host's tailnet address
(`http://<machine>.<tailnet>.ts.net:31337/` or its Tailscale IP) from any other
device on the tailnet. Point the CLI at the server with `PARLAY_SERVER` (defaults to
the Pulse address in this fleet; set it to the host's tailnet address from a
different machine), or persist a default so you don't have to re-export it every
shell session: `parlay remote set http://<machine>.<tailnet>.ts.net:31337`. See
`parlay remote --help` for the resolution precedence (env var still wins).

A few CLI verbs (`parlay help` lists them all):

```sh
parlay                       # live snapshot: subscribers, agents, last messages
parlay send --<agent> "hi"   # message an agent's channel
parlay monitor --agent <id>  # enrol + stream CHAT_MSG lines (agents arm this)
parlay listen --agent <id> --name <name>   # one-call self-enroll: register + announce + monitor
parlay reply "on it"         # an enrolled agent replies to its own channel
parlay alert "heads up"      # broadcast (auto-staggers on a large fleet)
parlay doctor                # this agent's self-diagnosis
```

Launch a background agent that shows up as a live tab:

```sh
parlay-spawn code-reviewer "Code Reviewer" "#c084fc" \
  "Review the diff in ~/code/foo and report findings." --cwd ~/code/foo
```

## Development

```sh
cd packages/<pkg> && bun test        # run a package's suite from its own directory
cd packages/client && bun build.ts   # build + deploy the panel bundle
```

There is no root `bunfig.toml`, so `bun test` at the repo root does not load the
happy-dom preload some packages need — always run a suite from inside its own
package. CI (`.github/workflows/ci.yml`) runs on every pull request and on
pushes to `main`, and does exactly that for the Go modules, the Bun packages,
and the hermetic shell harnesses.

Repo conventions worth knowing:

- **`bun install` installs this repo's git hooks.** The root `package.json`
  `prepare` script runs `git config core.hooksPath tools/hooks`. `pre-commit`
  runs for everyone: it enforces the 250-line limit below and auto-bumps
  `PA_VERSION`. `post-commit`/`post-merge` rebuild and deliver the panel bundle
  — a build plus a POST to a local Pulse server — and **do nothing at all unless
  you opt in**:

  ```sh
  git config --bool parlay.autobuild true   # enable; omit or set false to stay off
  ```

  That setting lands in the clone's shared `.git/config`, so **one `git config`
  covers every linked worktree of that clone** — it does not matter which
  checkout you run it from, and you never run it per worktree.

  Enabling is not delivering — two different things. Opted in, the hooks still
  deliver only from the repo's primary checkout; a commit in a linked worktree
  logs a skip and delivers nothing. Set `PARLAY_MAIN_CHECKOUT` to point them at
  a different tree. To drop all the hooks including `pre-commit`:
  `git config --unset core.hooksPath`.
- **250-line file limit** (pre-commit) — split a module into a subfolder + barrel
  index past the limit.
- **Two version axes** — the repo release `vX.Y.Z` git tag and the panel build
  `PA_VERSION` (`packages/client/src/version.ts`, auto-bumped per client change).
- Docs of note: `docs/COMMAND_DESIGN_CONTRACT.md` (the voice engine),
  `docs/CLI_VERBS_AND_EVENTS.md` (CLI verb authoring + the event fabric), and
  `docs/api-contract.md` (the HTTP API contract between client, CLI, and server).

## License

See repository. Alpha, single-owner project.
