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

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
