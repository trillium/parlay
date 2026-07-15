# Parlay relay + monitor split

Replaces N independent ~40MB bun `parlay monitor` pollers with **one** central
relay process plus **N** trivial per-agent monitors.

```
                       ┌──────────────────────────────────────────┐
   Pulse chat server   │                relay (Go)                │
   :31337              │  one upstream long-poll loop PER agent    │
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
`http://localhost:31337` (matches `bin/parlay`).

### Registry (monitor → relay, Unix control socket)

Socket: `<runtime>/relay.sock`. `<runtime>` defaults to `$TMPDIR/parlay`
(`/tmp/parlay` fallback).

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

## Run it

```sh
# 1. Build + start the ONE relay (footprint-irrelevant, static Go binary)
tools/relay/build.sh
tools/relay/parlay-relay &            # server 31337, runtime $TMPDIR/parlay

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

## Footprint & scaling math

Measured on this machine (`ps -o rss=`, macOS arm64):

| Component            | RSS (per process) | Instances |
|----------------------|-------------------|-----------|
| **relay** (Go)       | ~11 MB            | **1** (total) |
| **monitor** (`tail -F`) | ~1.2 MB        | N (one per agent) |
| old `parlay monitor` (bun) | ~40 MB      | N (one per agent) |

For N agents:

```
old:  N × 40 MB                       = 40N MB
new:  11 MB (relay) + N × 1.2 MB       = 11 + 1.2N MB
```

| N agents | old (bun) | new (relay+tail) | saved |
|---------:|----------:|-----------------:|------:|
| 1  | 40 MB  | 12.2 MB  | ~28 MB |
| 3  | 120 MB | 14.6 MB  | ~105 MB |
| 5  | 200 MB | 17 MB    | ~183 MB |
| 10 | 400 MB | 23 MB    | ~377 MB |

Crossover is immediate: even at N=1 the new split uses roughly a third of the old
memory, and the per-agent marginal cost drops from 40 MB to 1.2 MB. Replace the
table's measured numbers with the live `ps` readings from your machine — the
relay/monitor RSS is reported at build time and by `parlay monitor` startup.
