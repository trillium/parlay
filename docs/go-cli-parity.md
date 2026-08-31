# Go CLI parity survey (bead task-clvy)

Status of every TypeScript `parlay` CLI verb against the Go CLI at `tools/cli/`.

The TS CLI (`packages/cli/`) was **deleted in T-08** (commit `871b3f8f`, which
ported the last verb, `lavish-import`). The "TS path" column therefore refers to
the tree at `871b3f8f^` — reach it with
`git show 871b3f8f^:packages/cli/src/<file>` — and the parity universe below is
that tree's `index.ts` dispatch switch. `bin/parlay` builds and execs the Go
binary for every verb; there is no TS fallback path left.

Wiring: HTTP verbs speak `docs/api-contract.md` / `docs/api-contract.openapi.yaml`,
which both `packages/server` (TS) and `packages/go-server` implement; the CLI
targets whichever server `PARLAY_SERVER`/config points at. The go-server passed
SSE golden parity (PRs #186/#188/#189/#192), so `listen`-class streaming verbs
work against it unchanged. The contract's three `x-parlay-server: ts-only` ops
(`POST/GET /api/debug/input-timing`, `GET /parlay-ui.js`) are browser/panel-facing
and back **no CLI verb** — no verb needs the TS server.

## Verb table

Owner is the Go port workstream ticket (see `docs/agent-notes/go-cli-ticket-*.md`).

| Verb | TS path (`packages/cli/src/` @ `871b3f8f^`) | Go status | Owner / notes |
|---|---|---|---|
| (bare) `parlay` | `index.ts` → `commands.ts` | ported | B5 (`commands/status.go`) |
| `status` | `commands-status.ts` | ported | B5 (`commands/status_verb.go`) — **status-seam internals owned by fm/status-lift-2-\* sibling; hands off** |
| `subscribers` | `commands.ts` | ported | B3 |
| `agents` | `commands.ts` | ported | B3 |
| `agent-down` | `commands-agent-down.ts` | ported | B3 |
| `remote` | `commands-remote.ts` | ported | B3 |
| `spawn-account` | `commands-spawn-account.ts` | ported | fm/go-cli-1 (`commands/spawn_account.go`) — was the one dispatch gap left after T-08 (robots-ni5p): functionality existed as Go-only `defaults`, but the TS verb name was unreachable. Thin verb over `config.PersistedSpawnAccount`/`SetSpawnAccount`, TS output/exit contract. |
| `nickname` | `commands-nickname.ts` | ported | B3 |
| `send` | `commands.ts` | ported | B3 |
| `say` / `reply` | `commands.ts` (`cmdSay`) | ported | B4 (+ `internal/sayguard`) |
| `scratchpad` | `commands.ts` | ported | B4 |
| `identity` | `commands-identity/`, `identity-ephemeral.ts` | ported | B4 (`internal/identity`; park/reap/rename included; now bead-backed via gc unit 7, PR #191) |
| `alert` | `commands.ts` | ported | B3 (`commands/alert.go`) |
| `history` | `commands.ts` | ported | B3 |
| `monitor` | `monitor.ts`, `monitor-watchdog.test.ts` | ported | B4 (`internal/monitor`) |
| `listen` / `agent-up` | `listen.ts` | ported | B4 (`internal/monitor/listen.go`; consumes contract SSE — golden-parity-verified against go-server) |
| `stats` | `commands.ts` | ported | B3 |
| `doctor` | `commands-doctor.ts` | ported | B7 |
| `context-check` | `commands-context-check.ts` | ported | B5 |
| `robots-watch` | `commands-robots-watch/` | ported | B6 (`internal/robotswatch`) |
| `robots-tail` | `commands-robots-watch/` | ported | B6 |
| `health` | `commands-doctor.ts` | ported | B7 |
| `launch` | `commands` (launch) | ported | B9 |
| `variant` | `commands-variant.ts` | ported | B4 |
| `guard` | `commands-guard.ts` | ported | B4 |
| `crew-state` | `commands-crew-state.ts` | ported | B5 — **status-seam; hands off (fm/status-lift-2-\*)** |
| `supervise` | `commands-supervise.ts` | ported | B5 — **status-seam; hands off (fm/status-lift-2-\*)** |
| `teardown` | `commands-teardown.ts` | ported | B4 (+ teardown gates, `worktreeliveness`) |
| `drawdown` | `commands` (drawdown) | ported | B9 |
| `idle` | `commands` (idle) | ported | B9 |
| `lavish-import` | `lavish-import.ts`, `lavish-poll.ts` | ported | T-08 (completed the migration; `packages/cli` deleted in the same ticket) |
| `resolve-handoff` (not a dispatch verb) | `resolve-handoff.ts` | ported | B1/B8 (`internal/resolvehandoff` — death-window primitive consumed by `say`/`reply`, same as TS) |
| `say-guard` (not a dispatch verb) | `say-guard.ts` | ported | B1/B8 (`internal/sayguard` — same) |
| `help` / `--help` / `-h` | `help.ts` | ported | B3 (`internal/help`) |

**Deferred: none.** Every TS verb is reachable in the Go binary.

## Go-only verbs (no TS counterpart — nothing to port)

`defaults`, `spawn`, `stale`, `sweep`, `claim`, `mechanic`, `merge-gate`,
`route`, `branch-audit`, `commands`, `landed`, `city-scaffold`, `gc-spawn`,
`gc-nudge`, `gc-liveness`, `gc-resolve`. These postdate the TS CLI or were
born Go-side; the retired parity harness tracked them in `GO_ONLY_VERBS`
(see `docs/agent-notes/go-cli-ticket-b10-coverage-parity.md`).

## How parity was proven

The TS-vs-Go diff harness (`tools/cli/parity/run.sh`) validated the full
command surface before it was retired with `packages/cli` in T-08 (three real
Go bugs found and fixed — B10 notes). Since T-08, parity is carried by the Go
suite's hermetic per-verb tests (sandboxed `HOME`/`PARLAY_DATA_DIR`/
`PARLAY_STATE_HOME`), which encode the TS contract's output strings and exit
codes. There is no automatic flag-parity check anymore: **a dropped flag is a
hard exit, not a degraded flag** — when touching a ported verb, diff its flag
set against the TS source at `871b3f8f^` by hand.
