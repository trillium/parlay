# `parlay liveness` — which agents are silent, and why

**Code:** `tools/cli/internal/liveness` (the pure classifier),
`tools/cli/internal/commands/liveness.go` (the reads),
`liveness_render.go` (the table), `tools/cli/internal/relayctl` (the relay
trail reader).

```
parlay liveness [<agent-id>] [--agent <id>] [--silent-for <dur>] [--silent] [--json]
```

`parlay explain <agent-id>` answers "why is this agent not answering?" for **one**
agent. Nothing answered it for the fleet: `parlay launch` reports
live/ghost/offline but has no clock and no notion of last activity, and the
server's presence snapshot — the only per-channel activity record in the fleet —
was reachable only through verbs that each used one field of it. So an operator
asking "who has gone quiet" read `parlay launch`, then
`parlay subscribers --full`, then each agent's status file, and interleaved three
shapes by hand.

`liveness` does those reads once and prints one table. It is **read-only**: GET
routes on the server, the relay's read-only control socket, and local files. It
never sends, enrolls, retires, or writes — and a test asserts every relay request
it makes was a `GET` and that the ledger and status files are byte-identical
afterwards.

## Why the columns are separate

Four independent facts live behind "is this agent alive?", and collapsing them is
what produces the wrong repair:

| Column | Source | The distinction it keeps |
|---|---|---|
| `STATE` | `GET /api/chat/subscribers` ∩ the process table | A registration is a row nothing removes when a listener dies (robots-jkwc: 148 registered against 11 real listeners). A failed process-table probe is **not** evidence of a dead listener, so it never becomes `ghost`. |
| `HEARTBEAT` | the same snapshot's `presence` row | A stamp can **expire**; a row with no `lastSeen` and an absent row **cannot** — those are *absence*, and only the stamp shapes carry a parsed time at all. |
| `SILENT` | the newest dated record of **any** source | An agent that is working without talking has a stale channel stamp and a fresh status file. Measuring silence from the channel alone raises a false alarm on the healthiest agent in the fleet. |
| `LAST OBSERVED ACTIVITY` | channel record, status file, relay delivery trail | Which record actually saw the agent last, and how long ago. |

An `unknown` is always attributed to the source that failed. A missing record
never becomes a healthy value, and no unmeasurable question is answered with `0`.

## Degraded modes, verbatim

Every one of these is a real run of the built binary against a fixture: a
fixture chat server, a fixture relay runtime dir holding a real `delivery.log`,
and fixture agent homes. The agents are fixtures with no listener process, so
they correctly read `ghost` — that is the process-table half doing its job.

### A. Server up, relay down, ledger on disk, local status files present

```
parlay liveness — 4 agent(s) in the fleet · silence window 10m
server          http://127.0.0.1:34517
relay runtime   /tmp/plv.DPg6yg/rt
sources
  registry + presence    read — GET /api/chat/subscribers answered — 4 registered agent(s), 3 presence row(s)
  process table          read — 0 live listener process(es) on this host
  relay                  unreachable — no answer at /tmp/plv.DPg6yg/rt/relay.sock — the relay is not running (or uses another runtime dir). Its delivery trail is a FILE and is still read below; only the relay's live state is unknown
  delivery ledger        read — 2 delivery event(s) read (oldest first); the relay's own rotation is recorded in the trail, so a shortened history says so

AGENT                STATE     HEARTBEAT              SILENT     LAST OBSERVED ACTIVITY
crew-3               ghost     no row                 3h00m      status "blocked" 3h00m ago
crew-4               ghost     expired (3h00m ago)    3h00m      channel activity 3h00m ago
crew-2               ghost     never observed         unknown    no dated record
crew-1               ghost     fresh (32s ago)        no         channel activity 32s ago

notes
  crew-3               state      registered with nothing listening on this host — a message sent to this channel is spooled for a reader that is gone
  crew-3               heartbeat  the server's snapshot has no presence row for this channel — it has never recorded activity on it
  crew-4               state      registered with nothing listening on this host — a message sent to this channel is spooled for a reader that is gone
  crew-4               heartbeat  the server's last recorded activity on this channel is 3h00m old — past the 10m window (expired, not absent)
  crew-4               home       no agent home for this id on this host, so its status file could not be consulted either — run this where the agent runs to see local activity
  crew-2               state      registered with nothing listening on this host — a message sent to this channel is spooled for a reader that is gone
  crew-2               heartbeat  a presence row exists with no lastSeen — the server has never observed activity on this channel (absent, not expired)
  crew-2               silence    no dated activity record exists (looked at: the server's presence row for this channel) — silence is unmeasurable here, not zero
  crew-1               state      registered with nothing listening on this host — a message sent to this channel is spooled for a reader that is gone

2 of 4 silent beyond 10m · 2 with no heartbeat record (never observed or no presence row)
next  parlay explain <agent-id> for one agent's whole story · parlay timeline --agent <id> for its history
```

Four rows, four different stories:

- **crew-4 — stale heartbeat, not a stale agent.** Its channel stamp is three
  hours old, so `HEARTBEAT` is `expired`; the row still says `SILENT 3h00m`
  because that genuinely is the newest record anyone has for it (it has no home
  on this host).
- **crew-2 — absence, not staleness.** A presence row exists with no `lastSeen`,
  which is the server saying *I have never heard this channel*. `SILENT` is
  `unknown` with the note naming what was looked at, never `0`.
- **crew-3 — no row at all**, so the heartbeat is absent a different way, and its
  silence is measured from the local status file the operator can read.
- **crew-1 — healthy**, and the newest record is the channel stamp rather than
  the two-minute-old status file behind it.

### B. Relay down (the usual reason someone is asking)

The relay source line above is the whole point: the relay's live state is
`unknown`, and its delivery trail is still read because it is a **file**. In the
fixture, `crew-1`'s last observed activity comes from that trail.

### C. Server down, local records present

```
sources
  registry + presence    unreachable — no answer from http://127.0.0.1:1 — registration and channel activity are UNKNOWN, not absent (an unreachable server is not an empty fleet)
  process table          read — 0 live listener process(es) on this host
  relay                  unreachable — no answer at /tmp/plv.DPg6yg/rt/relay.sock — …
  delivery ledger        read — 2 delivery event(s) read (oldest first); …

AGENT                STATE     HEARTBEAT              SILENT     LAST OBSERVED ACTIVITY
crew-3               unknown   unknown                3h00m      status "blocked" 3h00m ago
crew-1               unknown   unknown                no         relay spooled 1m ago

notes
  crew-3               state      the server did not answer, so registration is unknown — this is not the same as offline
  crew-3               heartbeat  the server did not answer, so channel activity is unknown (not absent, and not fresh)
  …

1 of 2 silent beyond 10m · 0 with no heartbeat record (never observed or no presence row)
```

Exit code **0**: the local records answered, so this is bad news rather than no
news. `unknown` in `STATE` is deliberately *not* `offline` — the server never said
it is not registered.

### D. Nothing observable at all

```
parlay liveness — 0 agent(s) in the fleet · silence window 10m
…
  delivery ledger        absent — no ledger — this relay has never recorded a delivery event. That is NOT the same as 'nothing was delivered': an older relay build has no ledger at all

no agents to report — the server lists none and this host has no agent homes
parlay liveness: nothing was observable — the server did not answer at http://127.0.0.1:1 and no relay trail or agent home exists under /tmp/plv-empty.UXsSjv/agents
```

Exit code **1**. "Nothing to report" and "nothing was observable" are different
answers, and only one of them is a healthy fleet.

### E. Process table unreadable

```
  process table          unreadable — the process table could not be read, so a dead listener cannot be ruled out — registered agents are NOT reported as ghosts on a failed probe

AGENT                STATE     HEARTBEAT              SILENT     LAST OBSERVED ACTIVITY
crew-3               live      no row                 3h00m      status "blocked" 3h00m ago
crew-4               live      expired (3h00m ago)    3h00m      channel activity 3h00m ago
crew-2               live      never observed         unknown    no dated record
crew-1               live      fresh (31s ago)        no         channel activity 31s ago
```

Every registered agent stays `live` with a caveat note. A wrong `ghost` sends an
operator to `parlay agent-down` on a working agent, which costs more than a
missed stale row.

### Not applicable here: "missing cursor" and "spool with no writer"

Those two modes belong to `parlay explain` and `parlay timeline`, which read the
agent's spool and its resume cursor. **`liveness` deliberately does not read the
spool at all**: it answers *whether* an agent is being heard from, and opening N
spool files to answer that would make a fleet-wide command slower for a fact that
belongs to the per-agent surface. The nearest analogue here is the relay's
delivery trail, which is read as a file precisely so it answers with the relay
dead — see B.

## `--silent`, and why it is two conditions

`--silent` keeps the rows whose recorded activity is older than the window **or**
whose channel has no heartbeat record at all. Both are "not answering" in the only
two ways the records can say it; a row with a fresh heartbeat and a fresh status
line is neither.

## `--json`

Field names are stable, and the closed vocabularies (`state`, `heartbeat`,
`silence`, `last_source`, and each source's `state`) travel verbatim so a script
branches on them instead of parsing prose. Every `*_note` field carries the same
sentence the text mode prints, so the machine-readable form cannot quietly drop
the reason an answer is unknown.

## What it does not do

- No new HTTP route, so nothing to add to `internal/guard.GuardedPaths`; every
  read is an existing guarded `GET` or a local file.
- No writes anywhere, and no spool/cursor reads (see above).
- It does not decide whether an agent *should* be running. A `done` agent that is
  quiet is a correct `done` agent; that judgement is `parlay sweep`'s and
  `parlay stale`'s.
