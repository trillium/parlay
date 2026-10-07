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
| `STATE` | `GET /api/chat/subscribers` ∩ the process table, falling back to the roster **file** (`agents.json`) when the server does not answer | A registration is a row nothing removes when a listener dies (robots-jkwc: 148 registered against 11 real listeners). A failed process-table probe is **not** evidence of a dead listener, so it never becomes `ghost`. The file fallback answers "who is enrolled" from the server's last write and never "who is talking"; the note on the row says which source it was. |
| `HEARTBEAT` | the same snapshot's `presence` row | A stamp can **expire**; a row with no `lastSeen` and an absent row **cannot** — those are *absence*, and only the stamp shapes carry a parsed time at all. Presence is never written to disk, so the file fallback cannot answer this column and says so. |
| `SILENT` | the newest dated record of **any** source (`resumed` and `delivery-ended reason=shutdown` excluded — see F) | An agent that is working without talking has a stale channel stamp and a fresh status file. Measuring silence from the channel alone raises a false alarm on the healthiest agent in the fleet — and measuring it from the RELAY's own restart would raise a false all-clear across the fleet. |
| `LAST OBSERVED ACTIVITY` | channel record, status file, relay delivery trail | Which record actually saw the agent last, and how long ago. A relay-process event (F) is not a sighting of the agent: it is excluded from both clock columns and named on the row's note instead. |

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

### C. Server down, roster on disk

The server keeps its roster in a file (`agents.json`, a full snapshot rewritten
on every change), so a dead server does not un-write it. `liveness` reads it —
and says that it did, because `STATE` answered from a *file* is not the same
claim as `STATE` answered by the running server, and because `HEARTBEAT`
**cannot** come from that file: the server keeps presence in memory only,
deliberately, since a connection count that survived a restart would be lying.
Verbatim against a private fixture with a dead server:

```
sources
  registry + presence    unreachable — no answer from http://127.0.0.1:1 — registration and channel activity are UNKNOWN, not absent (an unreachable server is not an empty fleet)
  registry (disk)        read — read /tmp/pdoc.w9fTru/state/agents.json because the server did not answer — 2 agent(s) in the roster the server last persisted, so registration below comes from DISK; presence is never written to disk, so every heartbeat stays unknown
  process table          read — 0 live listener process(es) on this host
  relay                  unreachable — no answer at /tmp/pdoc.w9fTru/rt/relay.sock — …

AGENT                STATE     HEARTBEAT              SILENT     LAST OBSERVED ACTIVITY
crew-2               ghost     unknown                unknown    no dated record
crew-1               ghost     unknown                no         status "working" 1s ago

notes
  crew-2               state      listed in the server's on-disk registry with nothing listening on this host — a message sent to this channel is spooled for a reader that is gone
  crew-2               heartbeat  the server did not answer, so channel activity is unknown (not absent, and not fresh); presence is kept in memory by the server and is never written to disk, so there is no heartbeat record to fall back on
  …
```

What the fixture shows on purpose:

- `STATE` is an answer again because the process table is a **local**
  measurement: a rostered agent with nothing listening here is a `ghost`
  whether or not the server is up. The note names the substitution every time
  it is used.
- `HEARTBEAT` stays `unknown`, and the note says *why* (`presence is kept in
  memory… never written to disk`) instead of leaving a bare `unknown` that reads
  like a gap in the command.
- The middle of the honest spectrum is kept: an id that is **not** in that
  roster is `unknown`, never `offline` — which state directory the server runs
  with is a separate configuration point from the one this CLI reads, and only a
  live server settles it. `offline` is reserved for a server that answered and
  did not list the agent.

### C2. Server down, and no roster on disk

```
  registry (disk)        absent — no roster file at /tmp/pdoc2.3uqaIF/state/agents.json and the server did not answer — either it has never enrolled an agent or it runs with a -state-dir other than /tmp/pdoc2.3uqaIF/state. Not the same as 'no agent is registered'

AGENT                STATE     HEARTBEAT              SILENT     LAST OBSERVED ACTIVITY
crew-1               unknown   unknown                no         status "working" 0s ago
```

Every `STATE` is `unknown`, exactly as before this fallback existed. The absence
of the file is named, and it is not an empty fleet.

### C3. Roster present but unreadable

```
  registry (disk)        unreadable — could not read it (parse /tmp/pdoc2.3uqaIF/state/agents.json: invalid character 'o' in literal null (expecting 'u')) — the file is there and what it holds is unknown, not empty

AGENT                STATE     HEARTBEAT              SILENT     LAST OBSERVED ACTIVITY
crew-1               unknown   unknown                no         status "working" 0s ago
```

The parse failure is reported as itself. Reporting it as an empty roster would
be the one failure mode this whole surface exists to prevent.

### C4. Target is another machine

`agents.json` belongs to a HOST, not to the URL the CLI targets, so a target on
another machine makes this host's file another machine's roster. The fallback
declines and says why — and no agent from that file appears in the table:

```
  registry (disk)        not-this-host — not consulted — the CLI targets another machine, whose registry lives with it; /…/state/agents.json belongs to this host and is not that server's roster
```

### C5. Relay running, but its `/health` reports no bindings

An older relay build answers `/health` with its `ok` flag alone — the live relay
on this box does exactly that. That is not the end of the answer: `/agents`
carries the same two values from the same relay fields, and this verb reads both
routes, so a binding the relay does report is printed (with the route it came
from) rather than as `unknown`. Real output against the live fleet:

```
  relay                  read — up — polling http://macbook:31337, runtime /var/folders/…/T/parlay (this relay's /health omitted the server and runtime; its /agents answer reported it) · WARNING this relay polls http://macbook:31337, NOT the server this CLI targets (http://localhost:4242)
```

Two things follow, and both matter more than the wording. First, `polling
unknown` here would have been an *invented* unknown — the fact was in a response
this command had already read. Second, the warning is a comparison, and reading
only `/health`'s empty string silently dropped it: a relay pointed at another
server is the registered-but-deaf failure, the one case where an agent looks
live and receives nothing.

When **both** routes are silent, the line says so once and keeps the value
unknown. Verbatim against a private fixture whose `/health` is `{"ok":true}`
and whose `/agents` reports no bindings either:

```
  relay                  read — up — polling unknown, runtime unknown — neither its /health nor its /agents answer reported the server or its runtime dir, so neither is known — which server it polls is UNKNOWN, not a mismatch
```

And when the `/agents` route was never read (it did not answer), that is a
different, weaker fact, and it reads differently: `its /health reported neither,
and its /agents answer was not read`. "Nobody asked" must not borrow the wording
of "the relay refused", so the two are pinned apart by a unit test.

Before this work the same running relay printed `up — polling , runtime ` — two
empty strings where a sentence expected values, which reads as a measurement
rather than as a question the relay did not answer.

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

### F. The relay restarted — why a fresh ledger row is not agent activity

A relay restart writes two durable rows for **every** channel it was polling:
`delivery-ended reason=shutdown` (it stopped) and `resumed` (its spool replay
brought the channel back). Both name the agent, so both used to be candidates
for that agent's `LAST OBSERVED ACTIVITY` — and a fleet that had been deaf for
three hours would have reported as active two minutes ago. That is the
false-healthy direction this table exists to avoid, so a **relay-process event
is not counted as the agent's activity**, and the exclusion is stated on the row
rather than left invisible. Real output of the built CLI against the private
fixture (server refused, relay socket absent, this agent's spooled traffic three
hours old):

```
AGENT                STATE     HEARTBEAT              SILENT     LAST OBSERVED ACTIVITY
crew-1               unknown   unknown                3h00m      relay spooled 3h00m ago

notes
  crew-1               state      the server did not answer, so registration is unknown — this is not the same as offline
  crew-1               heartbeat  the server did not answer, so channel activity is unknown (not absent, and not fresh)
  crew-1               relay      the relay resumed polling this channel when it started at 2026-10-07T11:40:36Z — that is the RELAY's own event, not this agent's activity, so it was NOT counted toward the silence above: a restart writes one for every channel it was polling, and counting it would report a deaf fleet as freshly active
```

`SILENT` stays `3h00m` — measured from the agent's own traffic — instead of
collapsing to `0s` because the relay came back. What is excluded, precisely:

| event | counted as this agent's activity? |
|---|---|
| `spooled`, `spool-failed` | yes — the relay handed this channel a message, or failed to |
| `delivery-ended` with `channel-gone` or `unregister` | yes — this channel really ended, and that is the last thing known about it |
| `delivery-ended` with `shutdown` | **no** — the relay process stopped, not the channel |
| `resumed` | **no** — the relay's boot re-registered the channel; nothing says the agent is alive |
| `started`, `rotated` | not applicable — they name no agent at all |

The relay's live state is still reported (`STATE`, `relay enroll`, and the
`relay` source line), and `parlay timeline --outcome started,resumed` is the
query for "did the relay restart, and what came back?". The `relay_note` field
carries the same sentence in `--json`.

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
- It never treats the roster file as presence. `agents.json` is read only when
  the server did not answer, only when the target is this host, and only for the
  registration half — `HEARTBEAT` stays `unknown` with the reason on the note.
  See `tools/cli/internal/agentregistry`.
- It does not decide whether an agent *should* be running. A `done` agent that is
  quiet is a correct `done` agent; that judgement is `parlay sweep`'s and
  `parlay stale`'s.
