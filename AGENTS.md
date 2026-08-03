# Project agent memory

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

- Add durable project-specific notes here as they are discovered through real work.

## `packages/server` is a symlink into the live Pulse install

`packages/server/src/*` (all files except `package.json`) are symlinks into
`~/.claude/PAI/PULSE/modules/chat` — the actual code that runs live inside
Pulse. There is one copy of the source; edits from either path hit the same
file. See `packages/server/README.md` for the rationale and the known
`tools/split-test` tradeoff (per-branch server testing no longer works since
every branch resolves to the same external PULSE code).

**As of 2026-08-01, every file in that chain is a broken self-referential
symlink loop** (verify with `python3 -c "import os; print(os.path.realpath(p))"`
— it resolves to a path `os.path.exists()` reports `False` for). Confirmed on
both a disposable treehouse worktree and the primary checkout at
`~/code/parlay`, so it is not worktree-specific. No file under
`packages/server/src/` can currently be read or edited through normal tooling
until someone fixes the symlinks directly under `~/.claude/PAI/PULSE` (outside
any git worktree — not something an isolated agent should attempt). New
files with names that don't already exist in that farm are unaffected (Write
creates a plain file), but they can't be wired into `router.ts`/`index.ts`
until the loop is fixed. `packages/server/src/debug-log.ts` was added this
way — a standalone, not-yet-wired handler with wiring instructions in its
header comment.

**`packages/client`'s `build.ts` has a live side effect**: every successful
build POSTs to `http://127.0.0.1:31337/api/chat/reload`, and that port is the
captain's real, live local Pulse server — not sandboxed per worktree. Running
`bun run build` / `bun build.ts` in `packages/client` from *any* environment
that shares the host's network namespace (including disposable pool
worktrees) force-reloads the captain's actual connected clients. The built
bundle itself lands harmlessly in the invoking worktree's own `dist/` (and
gitignored `pulse-agent.js`/`plugins/`), so this doesn't ship broken code —
but it does interrupt whatever the captain is doing. Prefer `bun test` or a
scoped `bun build src/<file>.ts --outdir=<tmp>` (no `build.ts`) to validate
client changes without triggering the reload beacon.

`packages/cli` talks to whatever server is running over HTTP. Target resolution
(`serverUrl()` in `packages/cli/src/config.ts`): `PARLAY_SERVER` env var >
persisted `$PARLAY_STATE_HOME/config.json` (default `~/.parlay/config.json`,
`"server"` key, managed via `parlay remote set/clear`) > coded default
`http://localhost:4242`. `parlay doctor` reports which source is active. The
CLI does not import `packages/server` as code, so its functionality is
independent of the symlink structure below.

## Remote debug log + on-screen mobile console (phone-only bug triage)

`packages/client/src/debug-log.ts` captures `window.onerror`,
`unhandledrejection`, `console.error`/`warn`, and explicit `logTrace()` calls
(used in `thread-scroll.ts` and the `#pa-jump` handler in `jump-button.ts`), batches
them, and POSTs to `${CHAT_BASE}/debug-log` (i.e. `/api/chat/debug-log`).
Disable with `?paDebug=0` or `localStorage['pa-debug-log']='0'`. The server
handler (`packages/server/src/debug-log.ts`, not yet wired — see the section
above) appends formatted lines to `$PARLAY_STATE_HOME/debug.log` (default
`~/.parlay/debug.log`); once wired, read it with `tail -f ~/.parlay/debug.log`.
Disable server-side with `PARLAY_DEBUG_LOG=0`.

`packages/client/src/mobile-console.ts` lazy-loads `eruda` from a CDN for an
on-screen console on the phone itself. Toggle via `?paConsole=1` in the URL
(sticky, persists to localStorage) or a ~600ms long-press on the drawer
trigger button. Default off, zero bundle cost when unused.

## `bun test` only works from inside a package directory

There is no root `bunfig.toml`, so running `bun test` from the repo root
does not preload `@happy-dom/global-registrator` for packages whose tests
touch `document`/`window` (e.g. `packages/client`, `packages/input`) — those
tests fail with `ReferenceError: document is not defined` at the root even
though they pass cleanly run from their own directory (`cd packages/X && bun
test`). This predates any one package; always run a package's tests from
inside that package when validating.

## Publishable `@parlay/*` packages (npm scope not yet claimed)

This section covers npm packages under `packages/` (those with a
`package.json`); Go modules like `packages/go-server` (no `package.json`)
are outside this private/publishable dichotomy entirely. Every npm package
under `packages/` is `private: true` except `packages/input`
(`@parlay/input`) and `packages/parlay-input` (unscoped `parlay-input`,
holds the bare name and re-exports `@parlay/input`) — these are scaffolded
for eventual publishing but have never actually been published, and the
`@parlay` npm scope itself has not been claimed. Neither `bun build` nor
`tsc` alone produces a complete publishable output for a TS package here:
`bun build.ts` emits the JS bundle to `dist/`, and a package-local
`tsc --emitDeclarationOnly` (via a package-local `tsconfig.json`) emits the
`.d.ts` — `bun run build` runs both. `packages/server` was evaluated and is
not reasonably publishable: it's a `bun serve()` application with side
effects on import (opens a port, touches disk), not an importable library,
and its source is symlinked into the user's live personal Pulse install
(see the section above) — it remains `private: true`.

`packages/client` also gained `main`/`module`/`exports`/`types` fields and a
`dist/index.js` build target (`src/lib.ts`), but stays `private: true` — this
is a same-monorepo/workspace library entry point for external host apps (e.g.
herdr-web) importing the server-eval dispatcher, not npm publishing prep.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
