# relay — central Parlay fan-out

One Go process replaces N independent bun poll loops. It holds one upstream
long-poll loop per registered agent against the Pulse chat server and appends
each inbound user message to that agent's private spool file. Each agent's
`parlay monitor` just tails its spool.

## Build

```sh
./build.sh            # → tools/relay/parlay-relay (git-ignored)
```

Requires Go ≥ 1.26. The binary is static (`CGO_ENABLED=0`), stripped (`-s -w`).

## Run

```sh
./parlay-relay                                   # server=http://localhost:31337, runtime=$TMPDIR/parlay
./parlay-relay -server http://localhost:31337    # explicit server
./parlay-relay -agents main-agent,resume         # pre-register agents at startup
PARLAY_SERVER=http://localhost:31337 ./parlay-relay
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
| `/register`   | POST | `{"agent":"<id>"}` | `{"ok":true,"agent":"<id>","spool":"<path>"}` |
| `/unregister` | POST | `{"agent":"<id>"}` | `{"ok":true,"agent":"<id>"}` |
| `/agents`     | GET  | — | `{"agents":[...],"server":"...","runtime":"..."}` |
| `/health`     | GET  | — | `{"ok":true}` |

`register` is **idempotent**: a second register of a live agent returns its
existing spool and does not start a second loop. Agent ids must be kebab-slugs
(`^[a-z0-9]+(-[a-z0-9]+)*$`, ≤128 chars) — enforced so a spool path can never
escape the runtime dir.

Talk to it with curl:

```sh
SOCK="$TMPDIR/parlay/relay.sock"
curl -s --unix-socket "$SOCK" -X POST http://relay/register   -d '{"agent":"main-agent"}'
curl -s --unix-socket "$SOCK"          http://relay/agents
curl -s --unix-socket "$SOCK" -X POST http://relay/unregister -d '{"agent":"main-agent"}'
```

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
