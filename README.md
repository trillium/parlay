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
turnkey `open this URL and see the panel` path here yet. To run the UI you build the
bundle (`cd packages/client && bun run build`, which writes
`dist/parlay-agent.js`), serve it yourself, and reverse-proxy `/api/chat/*` to the
parlay server. That wiring is not documented here yet, and it is the main gap
between this repo and the demo.

**Tailscale is optional.** Nothing requires it. It is simply how the author reaches
the host from a phone; a LAN address or any other private tunnel works the same way.

## Quickstart (local only — no Pulse, no tailnet)

Prereqs: [Bun](https://bun.sh) and [Go](https://go.dev) 1.26+ (the CLI is Go; `bin/parlay`
builds it for you on first run).

```sh
git clone https://github.com/trillium/parlay && cd parlay
bun install                                   # also wires the git hooks (core.hooksPath tools/hooks)
```

**1. Start the server.** It listens on `:4242` and owns `/api/chat/*`:

```sh
cd packages/server && PARLAY_DATA_DIR=~/.parlay/data PAI_DIR=~/.parlay/pai-scratch bun run start
```

Both variables in that command matter, and here is why.

> **⚠️ `PARLAY_DATA_DIR` is not optional. Without it the server writes to — and can
> destroy — existing state.** Unset, it does not use one directory; it writes to two
> production locations:
>
> - **`~/exchange`** — chat history, draft, settings, agent channels, uploads.
> - **`$PAI_DIR/MEMORY/STATE`**, default **`~/.claude/PAI/MEMORY/STATE`** — the agent
>   registry (`parlay-agents.json`) and the session→channel map
>   (`parlay-session-channels.json`).
>
> That second one is the dangerous half, because a Claude Code / PAI user already has
> that directory: the server runs a prune sweep against that registry at boot, so
> starting it unconfigured mutates a live registry rather than an empty one.
> `packages/server/src/paths.ts` exists because of a real incident where exactly this
> happened and two live chat channels were deleted. `PARLAY_DATA_DIR` relocates all
> of it, flat, into the one directory you name.

`PAI_DIR` is pointed at an empty scratch directory for a second reason: it is both a
write target and a read target. Besides the registry above, the server tails that tree
for agent-activity events and folds every one of them into your chat history — so on a
machine that has a real `~/.claude/PAI`, leaving `PAI_DIR` unset fills a brand-new
instance with unrelated live agent turns. Setting both variables, as the command
does, is what fully protects the real directory. See
[`packages/server/README.md`](packages/server/README.md) for the full config surface.

**2. Point the CLI at it**, in another shell. Every command from here on is written
relative to the **root of the clone** — step 1 left the first shell inside
`packages/server`, and a brand-new shell starts in your home directory, so `cd` to the
clone first:

```sh
cd /path/to/parlay                         # the directory you cloned into above
export PARLAY_SERVER=http://localhost:4242
```

Export it in every shell you run `./bin/parlay` from. `PARLAY_SERVER` in the
environment always wins over the address `parlay remote set` persists to
`~/.parlay/config.json`, and the `bin/parlay` wrapper sets `PARLAY_SERVER` to
`http://localhost:31337` for you when it is unset — that is the port the author's own
page host happens to listen on, so exporting `:4242` above is what points the CLI at
the standalone server you just started. Nothing in this Quickstart needs anything
listening on `:31337`.

**3. Talk to it:**

```sh
./bin/parlay                               # live snapshot: subscribers, agents, last messages
./bin/parlay send --demo --force "hello"   # message the 'demo' channel
./bin/parlay history 5                     # read it back
./bin/parlay doctor                        # self-diagnosis: server reachable? identity set?
```

`send` normally refuses a target that isn't in the live agent registry; `--force`
seeds a channel before its agent has registered, which is exactly the case here.

That round-trip is the whole substrate. From here:

```sh
./bin/parlay reply --agent demo "on it"           # posts an agent-role message into history; channel routing needs a spawned agent's context
./bin/parlay alert "heads up"                     # broadcast to every agent
./bin/parlay help                                 # every verb
./bin/parlay monitor --legacy-poll --agent demo   # stream a channel; runs until Ctrl-C, so give it a second shell
```

`--legacy-poll` polls natively in Go and needs nothing beyond the server. `listen` —
one-call self-enrolment: register, announce, then stream — accepts the same flag and
takes the same native path, so `listen --agent demo --name Demo --legacy-poll` gives
you a live enrolled agent on a fresh clone too.

*Without* that flag, both verbs go through a relay binary that is gitignored and that
neither `bun install` nor `bin/parlay` builds; run `tools/relay/build.sh` first or they
exit 1 with `relay is not up and could not be started`. Mind bare `listen` especially:
it registers and announces with the server *before* it starts the relay, so on a fresh
clone it leaves an agent that looks enrolled and healthy but can never receive
anything.

Launch a background agent that shows up as a live tab (needs a
[Claude Code](https://claude.com/claude-code) install and the
[herdr](https://github.com/trillium/herdr) terminal it spawns into):

```sh
./bin/parlay-spawn code-reviewer "Code Reviewer" "#c084fc" \
  "Review the diff in ~/code/foo and report findings." --cwd ~/code/foo
```

To reach it from your phone, expose the host — Tailscale, LAN IP, or a private
tunnel — and export `PARLAY_SERVER` as that address instead of `localhost`.

**The chat API is unauthenticated by design** (that is how the CLI and plain `curl`
work — see the header of `packages/server/src/guard.ts`), so anything that can reach
the port can post into a live agent's turn. Expose it only over a private network —
a tailnet, a VPN, or a LAN you control — never a public tunnel or a port forwarded
to the internet.

## Layout

A [Bun](https://bun.sh) workspace monorepo, plus several standalone Go modules.
This table is a newcomer's map of the parts you need first, not a complete index
of every module in the repo:

| Package | What it is |
|---|---|
| `packages/server` | The Bun server that owns `/api/chat/*`: chat history, SSE, the long-poll feed the relay consumes, the server-side-eval relay, upload/link handling. Runs standalone on `:4242`. |
| `tools/relay` | The standalone per-agent relay daemon — its own Go module, built by `tools/relay/build.sh`. Fans the server's `/api/chat/poll` feed out to enrolled agents; `parlay monitor`/`listen` need it unless you pass `--legacy-poll`. |
| `packages/go-server` | An in-progress Go rewrite of the same HTTP/SSE surface. See [`docs/api-contract.md`](docs/api-contract.md). |
| `packages/client` | The chat panel — tabs, presence, message rendering, TTS/speech playback, annotations. Built as a browser bundle; needs a host that serves it same-origin with the API. |
| `tools/cli` | The Go `parlay` command surface — `reply`/`say`, `monitor`, `identity`/`scratchpad`/`handoff`, `alert`, `doctor`/`health`, and more. `bin/parlay` builds and execs this binary. |
| `packages/cli` | The original TS command surface. Superseded by `tools/cli`; kept only for `lavish-import`, which `bin/parlay` still routes here pending a Go port. |
| `packages/eval-engine` | A compiled Go (RE2) engine that matches spoken/typed phrases to a closed set of panel actions — the voice layer. |
| `packages/input` | `parlay-input` — a self-contained, framework-agnostic DOM input wrapper for wiring your own UI input to a parlay server. The one publishable npm package; no dependencies. |

Agent-facing entry points live in `bin/` (`parlay`, `parlay-spawn`, …).

## Development

```sh
cd packages/<name> && bun test    # a TS package (server/client/cli/input), from inside it — see note below
cd tools/cli && go test ./...     # the Go CLI
```

There is no root `bunfig.toml`, so `bun test` at the repo root does not load the
happy-dom preload some packages need: DOM-touching suites fail there with
`ReferenceError: document is not defined` even though they pass in-package —
always run a suite from inside its own package. CI
(`.github/workflows/ci.yml`) runs on every pull request and on pushes to
`main`, and does exactly that for the Go modules, the Bun packages, and the
hermetic shell harnesses.

Repo conventions worth knowing:

- **250-line file limit** (pre-commit) — split a module into a subfolder + barrel
  index past the limit.
- **Two version axes** — the repo release `vX.Y.Z` git tag and the panel build
  `PA_VERSION` (`packages/client/src/version.ts`, auto-bumped per client change).
- **Build the panel bundle with `cd packages/client && bun run build`** (that runs
  `build.ts`, which writes `dist/parlay-agent.js`). On success it also POSTs a
  best-effort reload beacon to `127.0.0.1:31337`; if nothing is listening there it
  just logs that and moves on. If you *are* running a live server on that port, the
  build will force-reload its connected clients — use `bun test` or a scoped
  `bun build src/<file>.ts --outdir=<tmp>` when you only want to validate a change.

Docs of note: [`docs/api-contract.md`](docs/api-contract.md) (the HTTP contract
between client, CLI, and server), [`docs/COMMAND_DESIGN_CONTRACT.md`](docs/COMMAND_DESIGN_CONTRACT.md)
(the voice engine), and [`docs/CLI_VERBS_AND_EVENTS.md`](docs/CLI_VERBS_AND_EVENTS.md)
(CLI verb authoring — sound mechanics, but written against the retired TS CLI;
`tools/cli` is the live surface). Several docs in `docs/` describe integration with the author's
own agent fleet, some of it public and some not — [`docs/README.md`](docs/README.md)
says which is which.

## Contributing

This is an alpha, single-owner project moving fast, so there's no formal contribution
process yet. Issues and PRs are welcome, but expect interfaces to shift under you.

## License

[MIT](LICENSE) © Trillium Smith
