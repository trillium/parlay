# Publishable packages use flat, unscoped `parlay-<part>` names — the `@parlay` scope is never published

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


This section covers npm packages under `packages/` (those with a
`package.json`); Go modules like `packages/go-server` (no `package.json`)
are outside this private/publishable dichotomy entirely. The naming rule is
fixed: anything meant for npm uses a **flat, unscoped** `parlay-<part>` name,
and the `@parlay` scope is **never** published (the scope is unclaimed and
nothing should depend on claiming it — a real publish attempt under it returned
404). Every npm package under `packages/`/`tools/` now uses that flat scheme:
`parlay-cli` (`packages/cli`), `parlay-client` (`packages/client`),
`parlay-server` (`packages/server`), and `parlay-split` (`tools/split-test`,
whose package name now matches its `parlay-split` bin). Those four stay
`private: true` — the rename is repo-wide naming hygiene, not a decision to
publish them; publishing remains the captain's separate call. The `@parlay`
scope no longer appears in any manifest, import, config, or changeset.

The single publishable package today is `packages/input`, named
`parlay-input`. It is self-contained — a real client for parlay's REST + shared-SSE
input protocol, built directly from its own `src/index.ts`, declaring no
runtime dependencies — and configured for npm publishing (`publishConfig:
{ access: "public" }`, `.changeset/config.json` `access: "public"`, MIT
`LICENSE` on disk). The earlier two-package split (a private `@parlay/input`
plus a `packages/parlay-input` shim re-exporting it) is **gone**: `@parlay/input`
cannot be published (unclaimed scope), so it was collapsed into this one
package and the shim deleted. `parlay-input@0.1.0` was the broken shim; the
self-contained rewrite is `0.2.0`. Every other npm package under `packages/`
is `private: true`. Neither `bun build` nor
`tsc` alone produces a complete publishable output for a TS package here:
`bun build.ts` emits the JS bundle to `dist/`, and a package-local
`tsc --emitDeclarationOnly` (via a package-local `tsconfig.json`) emits the
`.d.ts` — `bun run build` runs both. `packages/server` was evaluated and is
not reasonably publishable: it's a `bun serve()` application with side
effects on import (opens a port, touches disk), not an importable library
(see the standalone-server section above) — it remains `private: true`.

`packages/client` also gained `main`/`module`/`exports`/`types` fields and a
`dist/index.js` build target (`src/lib.ts`), but stays `private: true` — this
is a same-monorepo/workspace library entry point for external host apps (e.g.
herdr-web) importing the server-eval dispatcher, not npm publishing prep.
