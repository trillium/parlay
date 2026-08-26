# `bun test` only works from inside a package directory

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


There is no root `bunfig.toml`, so running `bun test` from the repo root
does not preload `@happy-dom/global-registrator` for packages whose tests
touch `document`/`window` (e.g. `packages/client`, `packages/input`) — those
tests fail with `ReferenceError: document is not defined` at the root even
though they pass cleanly run from their own directory (`cd packages/X && bun
test`). This predates any one package; always run a package's tests from
inside that package when validating.
