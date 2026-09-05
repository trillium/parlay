# Parlay relay + monitor split

Replaces N independent ~40MB bun `parlay monitor` pollers with **one** central
relay process plus **N** trivial per-agent monitors.

```
                       ┌──────────────────────────────────────────┐
   parlay chat server  │                relay (Go)                │
   :4242               │  one upstream long-poll loop PER agent    │
        ▲   ▲   ▲      │  agent-id → poll loop  (in-memory registry)│
        │   │   │◄─────┤  appends CHAT_MSG lines to per-agent spool │
   poll(A) poll(B) …   │  control socket: register/unregister/…    │
                       └───────┬───────────────┬──────────────────┘
                               │ spool A        │ spool B
                       <rt>/A.chan       <rt>/B.chan
                               │                │
                        tail -F A        tail -F B      ← monitor = tail (~1.2MB)
                               │                │
                          stdout→harness   stdout→harness
```

The server's poll is channel-scoped (one message per call, filtered by
`channel=<agent>`), so the relay holds **one upstream loop per agent** — but it is
**one process** holding them all. That is the win: process count drops from N to
1+N, and the N monitors are `tail`, not bun.

## Wire contract

### Upstream (relay → Pulse server)

```
GET {server}/api/chat/poll?after=<lastId>&channel=<agent>
  → {"timeout":true}                              idle tick, poll again
  → {"id","role","text","ts",...}                 one user message
```

Returns at most one message per call; the loop advances `after=<lastId>`. A
channel-scoped poll auto-registers the agent server-side. Default server is
`http://localhost:4242` (matches `bin/parlay`).

### Registry (monitor → relay, Unix control socket)

Socket: `<runtime>/relay.sock`. `<runtime>` defaults to `$TMPDIR/parlay`
(`/tmp/parlay` fallback) **for the default server only** — a relay is a
per-runtime-dir singleton bound to one upstream server, so a non-default
`$PARLAY_SERVER` resolves to its own `<runtime>/srv-<hash>` and gets its own
relay. The monitor also reads `/agents` → `server` and refuses to `/register`
against a relay bound elsewhere. See `monitor/NOTES.md` § Upstream-server scoping
(robots-buu8) — without this, a sandbox enrolled into the live registry.

| Route | Method | Body | Response |
|-------|--------|------|----------|
| `/register`   | POST | `{"agent":"<id>"}` | `{"ok":true,"agent":"<id>","spool":"<path>"}` |
| `/unregister` | POST | `{"agent":"<id>"}` | `{"ok":true,"agent":"<id>"}` |
| `/agents`     | GET  | — | `{"agents":[...],"server":"...","runtime":"..."}` |
| `/health`     | GET  | — | `{"ok":true}` |

`register` is idempotent. Agent ids must be kebab-slugs.

### Channel (relay → monitor, spool file)

Path: `<runtime>/<agent>.chan`, append-only. One line per message:

```
CHAT_MSG|<id>|<role>|<text>\n
```

`<text>` newlines are flattened to spaces — each message is exactly one line.

`parlay monitor --agent <id> --notify-safe` caps each emitted line to a
notification-safe budget (`PARLAY_NOTIFY_BUDGET`, default 400 chars) and
appends a "fetch full text" pointer, so a harness Monitor tool's own display
truncation can't silently cut a long line mid-word. Off by default. See
`tools/monitor/NOTES.md`.

## Run it

```sh
# 1. Build + start the ONE relay (footprint-irrelevant, static Go binary)
tools/relay/build.sh
tools/relay/parlay-relay &            # server 4242, runtime $TMPDIR/parlay

# 2. Each agent runs its own thin monitor (enroll + tail -F, ~1.2MB)
parlay monitor --agent main-agent     # via the CLI (default path)
# or directly:
tools/monitor/parlay-monitor.sh --agent main-agent
```

Harness enrollment is unchanged:

```
Monitor({ command: "parlay monitor --agent <id>", persistent: true })
```

`parlay monitor --legacy-poll` keeps the old independent bun poll loop for the
global feed or environments without the relay running.

`parlay spawn` arms `parlay listen --agent <id>` instead, which also
registers the agent and announces "listening" before execing into this same
`runMonitor` loop — one call in place of the old register + reply + monitor
three-step. See `parlay help listen`.

## Footprint & scaling math

Measured on this machine (`ps -o rss=`, macOS arm64, live server on :31337):

| Component            | RSS (per process) | Instances |
|----------------------|-------------------|-----------|
| **relay** (Go)       | ~13.6 MB          | **1** (total) |
| **monitor** (`tail -F`) | 1.17 MB (1200 KB) | N (one per agent) |
| old `parlay monitor` (bun) | 33.8 MB     | N (one per agent) |

For N agents:

```
old:  N × 33.8 MB                        = 33.8N MB
new:  13.6 MB (relay) + N × 1.17 MB       = 13.6 + 1.17N MB
```

| N agents | old (bun) | new (relay+tail) | saved |
|---------:|----------:|-----------------:|------:|
| 1  | 33.8 MB  | 14.8 MB  | ~19 MB  |
| 3  | 101 MB   | 17.1 MB  | ~84 MB  |
| 5  | 169 MB   | 19.5 MB  | ~150 MB |
| 10 | 338 MB   | 25.3 MB  | ~313 MB |

Crossover is at N=2 (the fixed relay cost is paid back once two agents share it);
from N=3 on the savings widen fast. The per-agent **marginal** cost is the real
win: 1.17 MB instead of 33.8 MB — a ~29× reduction per additional agent. The
relay's fixed cost is amortized across the whole fleet.
