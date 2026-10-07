# relay — central Parlay fan-out

One Go process replaces N independent bun poll loops. It holds one upstream
long-poll loop per registered agent against the Go chat server and appends
each inbound user message to that agent's private spool file. Each agent's
`parlay monitor` just tails its spool.

## Build

```sh
./build.sh            # → tools/relay/parlay-relay (git-ignored)
```

Requires Go ≥ 1.26. The binary is static (`CGO_ENABLED=0`), stripped (`-s -w`).

## Run

```sh
./parlay-relay                                   # server=http://localhost:4242, runtime=$TMPDIR/parlay
./parlay-relay -server http://localhost:4242    # explicit server
./parlay-relay -agents main-agent,resume         # pre-register agents at startup
PARLAY_SERVER=http://localhost:4242 ./parlay-relay
```

The relay runs as a long-lived daemon. `SIGINT`/`SIGTERM` → clean shutdown
(cancels every poll loop, drains, removes the control socket). Exit 0 clean,
exit 1 on a fatal startup error (bad server URL, socket already bound, unwritable
runtime dir).

## Registry & control socket

The registry is an in-memory `agent-id → poll loop`. It is driven over a Unix
domain **control socket** at `<runtime>/relay.sock`:

| Route | Method | Body | Response |
|-------|--------|------|----------|
| `/register`   | POST | `{"agent":"<id>"}` (+ owner token, see below) | `{"ok":true,"agent":"<id>","spool":"<path>"}` (+ `"token"` on first claim) |
| `/unregister` | POST | `{"agent":"<id>"}` (+ owner token) | `{"ok":true,"agent":"<id>","found":true/false}` |
| `/agents`     | GET  | — | `{"agents":[...],"server":"...","runtime":"..."}` |
| `/health`     | GET  | — | `{"ok":true}` |
| `/audit`      | GET  | `?limit=N` (default 100, max 1000) | `{"entries":[{"ts","actor","action","agent"}]}` |
| `/delivery`   | GET  | `?limit=N` (default 100, max 1000), `?agent=<id>` | `{ok,enabled,exists,ledger,count,entries}` |

`register` is **idempotent per caller**: a second register presenting the same
owner token returns the existing spool and does not start a second loop.
Agent ids must be kebab-slugs (`^[a-z0-9]+(-[a-z0-9]+)*$`, ≤128 chars) —
enforced so a spool path can never escape the runtime dir.

**Per-caller identity.** The first `/register` for an id mints a random owner
token, returned once in the response (`"token"`). Re-registering or
unregistering that id requires it — as an `Authorization: Bearer <token>`
header or a `{"token":"<token>"}` body field — or the call is rejected
(409 on register, 403 on unregister) and the denial is audit-logged. Only
the token hash is stored (`<runtime>/owners.json`, 0600, survives restarts);
raw tokens are never logged. A successful `/unregister` releases ownership
so the id is claimable again. Internal registrations (startup `-agents`,
spool resume) never mint or check. `parlay-monitor.sh` persists its token as
`<runtime>/<agent>.token` and replays it on every enroll.

**Audit log.** Every `/register` and `/unregister` outcome — success and
denial — appends one JSON line to `<runtime>/audit.log` with who (`actor`:
owner-token fingerprint, never the raw token), what (`action`: `register` |
`unregister` | `register-denied` | `unregister-denied`), target (`agent`),
and when (`ts`, RFC3339 UTC).

Talk to it with curl:

```sh
SOCK="$TMPDIR/parlay/relay.sock"
curl -s --unix-socket "$SOCK" -X POST http://relay/register   -d '{"agent":"main-agent"}'
# → {"ok":true,...,"token":"<owner>"} — save it:
TOKEN="<owner>"
curl -s --unix-socket "$SOCK" -X POST http://relay/register   -d '{"agent":"main-agent"}' -H "Authorization: Bearer $TOKEN"
curl -s --unix-socket "$SOCK"          http://relay/agents
curl -s --unix-socket "$SOCK"          http://relay/audit?limit=5
curl -s --unix-socket "$SOCK"          'http://relay/delivery?agent=main-agent&limit=20'
curl -s --unix-socket "$SOCK" -X POST http://relay/unregister -d "{\"agent\":\"main-agent\",\"token\":\"$TOKEN\"}"
```

## Delivery ledger

The audit log answers enrollment; it says nothing about messages. The spool
file is where messages land but carries no timestamp (`CHAT_MSG|<id>|<role>|`, a
wire format `lastSpooledID` and the monitor's line reader both depend on), so
before the ledger the relay — the one component that knows *when* a message
reached an agent — recorded nothing about delivery. The ledger is that answer:
`<runtime>/delivery.log`, one JSON line per event, identifiers only.

```json
{"ts":"...","event":"spooled","agent":"a","msg":"m-1","role":"user"}
{"ts":"...","event":"spool-failed","agent":"a","msg":"m-2"}
{"ts":"...","event":"delivery-ended","agent":"a","reason":"channel-gone","spoolLines":1}
{"ts":"...","event":"rotated","reason":"size-cap"}
```

- `spooled` — appended to that agent's spool. This is the relay's delivery
  boundary, **not** proof the agent read it: nothing in the fleet acknowledges
  consumption, and a record that called this "delivered" would be the lie the
  ledger exists to remove.
- `spool-failed` — the append failed; the message did not reach the agent.
- `delivery-ended` — the channel stopped being polled, with `reason`
  (`channel-gone` | `unregister` | `shutdown`) and `spoolLines`, the count taken
  *before* the spool is tombstoned.
- `rotated` — the active file hit 8 MiB and the previous generation moved to
  `delivery.log.1`. Rotation is lossy, so it is recorded rather than silent.

Nothing free-form is stored — never a message body, path, or error string —
the same posture as the audit log, which is why this cannot become a second copy
of user data. Writes are best-effort like `audit()` and can never slow or fail a
delivery (pinned by `TestDeliveryLedgerUnwritableDoesNotBlockDelivery`).
`PARLAY_RELAY_DELIVERY_LOG=0` disables recording; `/delivery` then reports
`enabled:false`, and `exists:false` distinguishes a ledger that was never
written from one that is merely empty.

## Spool files

`<runtime>/<agent>.chan`, append-only. One line per message:

```
CHAT_MSG|<id>|<role>|<text>\n
```

Newlines in `<text>` are flattened to spaces so each message is exactly one line.
The relay creates the spool on register (so `tail -F` has a file immediately) and
appends with `O_APPEND` so a relay restart or a lagging monitor never loses lines.

## Upstream contract

`GET {server}/api/chat/poll?after=<lastId>&channel=<agent>` returns either
`{"timeout":true}` (idle tick — poll again) or one message
`{"id","role","text","ts",...}`. The server returns at most **one** message per
call, so each per-agent loop advances `after=<lastId>` to fetch the next. A
channel-scoped poll also auto-registers the agent server-side (grey tab until its
first reply). Per-request timeout is 45s (server long-poll is 30s); on any
upstream error the loop backs off `reconnectDelay` (2s) and retries — it never
dies on a transient failure, because a Parlay agent must survive a server bounce.

## Footprint

The relay is footprint-irrelevant: exactly ONE instance runs regardless of agent
count. The win is process count — 1 relay + N tiny monitors vs. the old N×~40MB
bun pollers. See `../RELAY_MONITOR.md` for the measured RSS math.
