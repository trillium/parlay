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

`packages/cli` talks to whatever server is running over HTTP
(`PARLAY_SERVER`, default `http://localhost:4242`) — it does not import
`packages/server` as code, so CLI functionality is independent of this
symlink structure.

## `bun test` only works from inside a package directory

There is no root `bunfig.toml`, so running `bun test` from the repo root
does not preload `@happy-dom/global-registrator` for packages whose tests
touch `document`/`window` (e.g. `packages/client`, `packages/input`) — those
tests fail with `ReferenceError: document is not defined` at the root even
though they pass cleanly run from their own directory (`cd packages/X && bun
test`). This predates any one package; always run a package's tests from
inside that package when validating.

## Publishable `@parlay/*` packages (npm scope not yet claimed)

Every package under `packages/` is `private: true` except `packages/input`
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
