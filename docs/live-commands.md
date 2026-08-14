# Live commands

> **What this is:** one server-side registry of the parlay command invocations
> that are running right now, and two renderers over it — the `parlay commands`
> CLI verb and the chat panel's live-commands view. Base path for every route
> below is `/api/chat`, the same base as the rest of
> [the HTTP API contract](./api-contract.md).
>
> **Read the [Coverage](#coverage--what-this-cannot-see) section before
> trusting an empty list.** This registry sees what reports itself and nothing
> else. It is deliberately honest about that rather than quietly complete.

---

## One registry, two renderers

The registry lives on the server (`packages/go-server/internal/store/commands.go`,
served by `internal/handlers/commands.go`). Neither renderer keeps its own
model of what is running:

| Surface | Reads | Source file |
| --- | --- | --- |
| CLI | `GET /api/chat/commands`, plus the SSE stream for `--watch` | `tools/cli/internal/commands/commands_view.go` |
| Panel | the `commands` / `command_update` SSE frames, plus `GET /api/chat/commands` on open | `packages/client/src/live-commands/` |

The two payloads are the same bytes, not merely the same idea: the SSE
`commands` frame carries exactly the array `GET /api/chat/commands` returns,
pinned by `TestSSEBurstAndReadEndpointCarryByteIdenticalCommands`. The wire
shape itself is pinned by a fixture that three test suites read —
`packages/go-server/testdata/live-commands.golden.json` — so a field renamed
on the server fails the server, CLI, and panel suites in the same commit.

---

## Registration: the CLI reports itself

**The Go CLI is the only thing that registers invocations.** `main.go` wraps
dispatch in `commandreport.Begin(verb, argv)`, which POSTs a start report,
heartbeats every 20s while the command runs, and POSTs an end report with the
exit code. The end report is wired through **two** paths so an ordinary failure
still closes the record:

- `httpc.Exit` — the CLI's single exit hook, which `httpc.Die` routes through.
  Wrapping it covers every fail-loud request in every verb without touching any
  of them.
- a `defer` in `main` — covers a normal return and a panic.

`PARLAY_COMMAND_REPORT=0` (also `off`/`false`/`no`) disables reporting entirely.

### The two alternatives, and why not

**A wrapper that reports on behalf of whatever it launches** (a `parlay run …`
shim, or a shell wrapper on `PATH`) would cover more than the Go CLI — shell
scripts, the TS CLI, anything. It was rejected because coverage would then
depend on *how* a command was invoked: everything launched directly, by a
hook, or by another script would silently fall outside it, and the view would
be partial in a way no one could predict from reading it. Self-reporting is
partial in a way you can state in one sentence.

**Server-side registration only** — the server records the work it originates,
and nothing else — is the most honest option and also nearly empty: almost
every parlay command is a client of the server, not a job the server starts.
It answers "what did the server kick off", which is not the question.

Self-reporting is the middle option: narrower than a wrapper, far wider than
server-only, and its blind spots are a fixed list rather than a function of
the call site.

---

## Coverage — what this cannot see

An empty list means **nothing reported**, not "nothing is running". Specifically,
the following are invisible here:

- **Anything that is not the Go CLI.** Shell entry points (`bin/parlay-spawn`,
  `tools/monitor/parlay-monitor.sh`, deploy scripts) and the retired TS CLI in
  `packages/cli` do not report.
- **Work the server does on its own** — SSE fan-out, sweeps, long-poll waiters.
  Those are not invocations and have no record here.
- **`parlay commands` itself.** The observer is excluded from its own output,
  so a read never shows at least one running command (itself) and `--watch`
  never shows a permanent entry for the watcher.
- **Anything run with `PARLAY_COMMAND_REPORT=0`.**
- **Anything that could not reach the server.** The first failed report
  disables reporting for the rest of that process, by design — see
  [Never a new failure mode](#never-a-new-failure-mode).

Both renderers print this limit in their empty state rather than leaving a
blank list to be misread.

---

## What is recorded, and what is never recorded

A parlay command line routinely carries message bodies, tokens, and absolute
paths. **The registry stores no free-form text at all.** A record holds:

| Field | Source | Notes |
| --- | --- | --- |
| `id` | reporter | random per invocation |
| `verb` | reporter | the subcommand, e.g. `listen` |
| `agent`, `channel` | `$PARLAY_AGENT_ID` / report | ids only |
| `flags` | reporter | flag **names** only, max 8 |
| `pid` | reporter | |
| `state` | server | `running` / `finished` / `failed` / `expired` |
| `startedAt`, `updatedAt`, `endedAt` | server | RFC3339 |
| `exitCode` | reporter | |
| `outcome` | reporter | a short token (`ok`, `error`, `no-heartbeat`) — never a message |
| `durationMs` | server | computed at read time |

Never recorded, in either layer: **raw argv, flag values, positional
arguments, message text, file paths, URLs, environment values, or an error
string.**

Redaction happens twice. The CLI strips values before sending
(`--token s3cr3t`, `--token=s3cr3t`, and a bare `s3cr3t` all contribute at most
`--token`), and the server sanitizes again on arrival — because the report
endpoints are unauthenticated, so the storage layer cannot trust its callers.
`outcome` is a token rather than free text for exactly this reason: an error
*string* is the field that would otherwise carry a path or a secret.

Pinned by `TestNoFlagValueEverReachesTheWire` (server),
`TestFlagNamesNeverCarryValues` and `TestStartPayloadCarriesNoPositionalsAtAll`
(CLI reporter), and `commandDetail`'s tests on both renderers.

---

## Lifecycle and reaping

Stale-entry reaping matters more than completeness: a permanently "running"
entry for a process that died is worse than a missing one, because it makes the
whole view untrustworthy.

1. **Start** — `POST /api/chat/command-start` creates a `running` record.
   Idempotent: a repeat for the same id updates rather than duplicating.
2. **Heartbeat** — `POST /api/chat/command-heartbeat` every 20s. A heartbeat
   for an id the server does not hold answers `{"ok":false,"unknown":true}`,
   and the reporter re-sends its start — which is how the view self-heals
   within one heartbeat after a server restart.
3. **End** — `POST /api/chat/command-end` sets `finished` or `failed`. An end
   with no preceding start still records the invocation, so the reporter never
   has to sequence its two POSTs.
4. **Reap** — a `running` record that has not heartbeated for
   `staleAfterMs` (90s) becomes `expired` with outcome `no-heartbeat`. This is
   the backstop for `SIGKILL`, a crashed process, or a pulled power cord —
   none of which can run an exit handler.
5. **Drop** — a terminal record is retained briefly (60s) so you can see how a
   command ended, then removed. The removal is broadcast as a `command_update`
   with state `dropped` so a long-lived panel prunes its map instead of growing
   forever on an append-only stream.

The registry is **in-memory only**, deliberately, for the same reason
`PresenceTracker` is: a record that survived a restart would claim a process is
running that this server has no way to confirm. It is also bounded
(`maxRecords`, 500).

Lifecycle tests: `TestStartedCommandAppearsOnTheReadEndpoint`,
`TestFinishedCommandLeavesTheRunningSet`,
`TestCommandThatDiesWithoutReportingIsReaped`.

---

## Wire contract

### `GET /api/chat/commands`

Sweeps first, so a poll is always current.

```json
{
  "ok": true,
  "now": "2026-01-01T12:00:30Z",
  "running": 2,
  "staleAfterMs": 90000,
  "commands": [ { "id": "…", "verb": "listen", "state": "running", … } ]
}
```

`commands` is ordered newest-first. Optional record fields are omitted when
empty; `durationMs` is always present.

### `POST /api/chat/command-start`

Request `{ id, verb, agent?, channel?, flags?, pid? }` →
`{ ok: true, id, state }`. Validation failures answer **200 with
`{"error": …}`**, matching `register-agent`: a reporter is a fire-and-forget
side channel and must never turn a working command into a failing one.

### `POST /api/chat/command-heartbeat`

Request `{ id }` → `{ ok: true, id, state }`, or `{ ok: false, unknown: true }`
if the server has forgotten that id.

### `POST /api/chat/command-end`

Request `{ id, state, exitCode?, outcome? }` → `{ ok: true, id, state }`.

### SSE events on `GET /api/chat/events`

Two new event names, both **additive** — they were added after the 17 names in
[the API contract](./api-contract.md), and an older client ignores unknown
frames:

- **`commands`** — the full array, sent once in the connect burst.
- **`command_update`** — one record whenever it changes state. A frame whose
  `state` is `dropped` carries only an `id` and means "forget this one".

Heartbeats are deliberately **not** broadcast: they carry no state change and
would be the noisiest thing on the stream.

---

## Security posture

The chat API has no authentication; that is unchanged here and is why every
mitigation below is about bounding damage rather than establishing identity.

- **Nothing existing was modified.** Every route above is new, and no existing
  response shape changed.
- **The three mutating routes require `POST` + `Content-Type:
  application/json`** (415 otherwise). A cross-origin CORS *simple* request can
  only send `text/plain`, `form-urlencoded`, or `multipart`; anything else must
  preflight, and this server answers no preflight. That is the same mechanism
  `packages/server/src/guard.ts` uses for the Bun server's mutating chat
  routes. It is CSRF-shaped, not authentication: a local process can still
  report whatever it likes, which bounds forgery to "something already running
  on this machine" — the thing the view claims to describe anyway.
- **The read endpoint stays world-readable**, matching `/api/chat/agents`. What
  it exposes is verbs, agent ids, and pids — no argv, no paths, no message
  text.
- **The payload is bounded** — 8 flag names per record, 500 records, sanitized
  tokens only — so a flood cannot grow memory without limit or smuggle text
  through.

---

## Never a new failure mode

A view of running commands must not be able to break the commands it watches,
and must not break the panel when the server does not have it.

- Every report has a 400ms timeout, swallows every error, and **the first
  failure disables reporting for the rest of the process** — so an unreachable
  server costs one short timeout, not one per report. A command whose work is
  entirely local still succeeds with no server at all.
- `parlay commands` against a server without the registry gets a 404, prints
  that the server does not expose one, and **exits 0**. Only a genuinely
  unreachable server is an error, matching every other read verb.
- The panel renders the same case as "unavailable" and touches nothing else.
  A dropped or malformed frame is ignored, never thrown.

---

## Using it

```
parlay commands                    # what is running now
parlay commands --all              # include recently finished/failed/expired
parlay commands --agent <id>       # narrow to one agent
parlay commands --verb <verb>      # narrow to one verb
parlay commands --json             # machine-readable, same state
parlay commands --watch            # follow over SSE (one connection, no polling)
```

In the panel: the **▷** button in the drawer header, or **▷ Live commands** in
the mobile action sheet. Both open a card over the thread listing the same
records, with ages ticking locally between server updates.
