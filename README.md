# Parlay

**Talk to your background coding agents from your phone.** Parlay gives every
long-running AI agent its own chat channel — dictate a message while you're walking
the dog, the agent picks it up, works, and replies to the same thread.

This is the project behind the **"Voice First: Code Anywhere"** demo: voice dictation
on a phone, driving autonomous terminal coding agents on a machine somewhere else.

> Status: **alpha**, single-owner. Interfaces move fast. Built for one person's
> workflow first — it works, but it has sharp edges and assumes you're comfortable
> running your own server.

---

## What you actually get

- **A per-agent chat channel.** Every enrolled agent gets its own tab. Messages route
  only to the agent they're addressed to.
- **Voice-first control.** A compiled phrase engine turns spoken commands into panel
  actions, and agent replies can be read back aloud with per-passage playback — so a
  whole review cycle can happen without a keyboard.
- **Durable identity + memory.** `identity`, `scratchpad`, and `handoff` persist
  across restarts, so an agent that blows its context window recovers *who it is* and
  *what it was doing* via the `identity → handoff → scratchpad` chain.
- **Spawn + supervise.** Launch a background agent that auto-enrols as a live tab, and
  drive event-based follow-ups.
- **Reachable from anywhere you can reach the host** — over your LAN, a
  [Tailscale](https://tailscale.com) tailnet, or just `localhost`.

## Requirements, honestly

**The CLI + server work standalone.** `bun install`, start the server, point the CLI
at it — no other services, no accounts, no tunnel. That's the path in the Quickstart
below and it is the one this repo fully supports.

**The web panel does not ship with a host.** The chat panel
(`packages/client`) is a browser bundle that expects to be served from the *same
origin* as the chat API, and the author serves it from a personal, unreleased page
host called Pulse. **Pulse is not open source and is not available** — so there is no
turnkey `open this URL and see the panel` path here yet. To run the UI you need to
serve `packages/client/dist/parlay-agent.js` yourself and reverse-proxy `/api/chat/*`
to the parlay server. That wiring is not documented here yet, and it is the main gap
between this repo and the demo.

**Tailscale is optional.** Nothing requires it. It is simply how the author reaches
the host from a phone; a LAN address or any other tunnel works the same way.

## Quickstart (local only — no Pulse, no tailnet)

Prereqs: [Bun](https://bun.sh) and [Go](https://go.dev) 1.26+ (the CLI is Go; `bin/parlay`
builds it for you on first run).

```sh
git clone https://github.com/trillium/parlay && cd parlay
bun install                                   # also wires the git hooks (core.hooksPath tools/hooks)
```

**1. Start the server.** It listens on `:4242` and owns `/api/chat/*`:

```sh
cd packages/server && PARLAY_DATA_DIR=~/.parlay/data bun run start
```

`PARLAY_DATA_DIR` puts every file it persists in one directory. **Leave it unset and it
writes to the author's production locations under `~/exchange` instead** — set it.
If you also happen to have a `~/.claude/PAI` directory, set `PAI_DIR` to somewhere
empty too: the server tails that tree for agent-activity events and will fold them
into your chat history. See [`packages/server/README.md`](packages/server/README.md)
for the full config surface.

**2. Point the CLI at it**, in another shell:

```sh
export PARLAY_SERVER=http://localhost:4242
# or persist it so you don't re-export every session:
./bin/parlay remote set http://localhost:4242
```

**3. Talk to it:**

```sh
./bin/parlay                          # live snapshot: subscribers, agents, last messages
./bin/parlay send --demo "hello"      # message the 'demo' channel
./bin/parlay history 5                # read it back
./bin/parlay doctor                   # self-diagnosis: server reachable? identity set?
```

That round-trip is the whole substrate. From here:

```sh
./bin/parlay monitor --agent demo     # enrol + stream incoming messages (agents arm this)
./bin/parlay listen --agent demo --name Demo   # one call: register + announce + monitor
./bin/parlay reply "on it"            # an enrolled agent replies to its own channel
./bin/parlay alert "heads up"         # broadcast to every agent
./bin/parlay help                     # every verb
```

Launch a background agent that shows up as a live tab (needs a
[Claude Code](https://claude.com/claude-code) install and the
[herdr](https://github.com/trillium/herdr) terminal it spawns into):

```sh
./bin/parlay-spawn code-reviewer "Code Reviewer" "#c084fc" \
  "Review the diff in ~/code/foo and report findings." --cwd ~/code/foo
```

To reach it from your phone, expose the host — Tailscale, LAN IP, or any tunnel —
and point the CLI at that address instead of `localhost`. `parlay remote --help`
explains the resolution precedence (env var wins over the persisted default).

## Layout

A [Bun](https://bun.sh) workspace monorepo, plus several standalone Go modules:

| Package | What it is |
|---|---|
| `packages/server` | The Bun server that owns `/api/chat/*`: chat history, SSE, long-poll, the per-agent relay, upload/link handling. Runs standalone on `:4242`. |
| `packages/go-server` | An in-progress Go rewrite of the same HTTP/SSE surface. See [`docs/api-contract.md`](docs/api-contract.md). |
| `packages/client` | The chat panel — tabs, presence, message rendering, TTS/speech playback, annotations. Built as a browser bundle; needs a host that serves it same-origin with the API. |
| `tools/cli` | The Go `parlay` command surface — `reply`/`say`, `monitor`, `identity`/`scratchpad`/`handoff`, `alert`, `doctor`/`health`, and more. `bin/parlay` builds and execs this binary. |
| `packages/cli` | The original TS command surface. Superseded by `tools/cli`; kept only for `lavish-import`, which `bin/parlay` still routes here pending a Go port. |
| `packages/eval-engine` | A compiled Go (RE2) engine that matches spoken/typed phrases to a closed set of panel actions — the voice layer. |
| `packages/input` | `parlay-input` — a self-contained, framework-agnostic DOM input wrapper for wiring your own UI input to a parlay server. The one publishable npm package; no dependencies. |

Agent-facing entry points live in `bin/` (`parlay`, `parlay-spawn`, …).

## Development

```sh
cd packages/<name> && bun test    # run a package's suite from inside it — see note below
cd tools/cli && go test ./...     # the Go CLI
```

There is no root `bunfig.toml`, so `bun test` at the repo root does not load the
happy-dom preload some packages need — always run a suite from inside its own
package. CI (`.github/workflows/ci.yml`) runs on every pull request and on
pushes to `main`, and does exactly that for the Go modules, the Bun packages,
and the hermetic shell harnesses.

Repo conventions worth knowing:

- **Run `bun test` from inside a package**, not the repo root. There is no root
  `bunfig.toml`, so DOM-touching suites fail at the root with
  `ReferenceError: document is not defined` even though they pass in-package.
- **250-line file limit** (pre-commit) — split a module into a subfolder + barrel
  index past the limit.
- **Two version axes** — the repo release `vX.Y.Z` git tag and the panel build
  `PA_VERSION` (`packages/client/src/version.ts`, auto-bumped per client change).
- **Don't run `packages/client/build.ts` casually** — on success it POSTs a reload
  beacon to the author's live server on `127.0.0.1:31337`. Use `bun test` or a scoped
  `bun build src/<file>.ts --outdir=<tmp>` to validate client changes.

Docs of note: [`docs/api-contract.md`](docs/api-contract.md) (the HTTP contract
between client, CLI, and server), [`docs/COMMAND_DESIGN_CONTRACT.md`](docs/COMMAND_DESIGN_CONTRACT.md)
(the voice engine), and [`docs/CLI_VERBS_AND_EVENTS.md`](docs/CLI_VERBS_AND_EVENTS.md)
(CLI verb authoring). Several docs in `docs/` describe integration with the author's
own private agent fleet — [`docs/README.md`](docs/README.md) says which is which.

## Contributing

This is an alpha, single-owner project moving fast, so there's no formal contribution
process yet. Issues and PRs are welcome, but expect interfaces to shift under you.

## License

[MIT](LICENSE) © Trillium Smith
