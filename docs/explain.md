# `parlay explain` — one agent's whole story

**Code:** `tools/cli/internal/commands/explain.go` (gather), `explain_delivery.go`
(the trail, from the socket or from disk), `explain_render.go` (one line per
fact), `registry_file.go` (the roster fallback, shared with `liveness`),
`tools/cli/internal/relayctl` (the read-only relay client), and
`tools/cli/internal/agentregistry` (the roster file reader).

```
parlay explain <agent-id>
```

The 2am question is **"why is this agent not answering?"** Answering it used to
mean running four commands — `parlay agents`, `parlay crew-state`,
`parlay commands --agent`, and something that read the relay's runtime dir by
hand — and then reading source to interpret them, because each surface knew one
slice and none could see the others. `explain` performs those reads once and
renders a single story, in the order an operator reads it.

It is **read-only**. It never sends, enrolls, or tears down. The relay client it
uses exposes GET routes only, so there is no mutation path to reach even by
mistake.

## The six sources, and what each answers

| Section | Source | Answers |
|---|---|---|
| `registration` | `GET /api/chat/subscribers`, falling back to the roster **file** (`agents.json`) | is it registered, name, colour. The server is asked first; when it does not answer the file it last persisted is read instead, and the line says so — a registration answered from a file is a persisted row, not a live answer |
| `channel` | the same snapshot's `presence` row | when its channel was last observed — and whether it ever was |
| `crew state` | the frozen `reconcileCrewState` contract | state + source + detail, **from the same registry read** `parlay crew-state` performs, so the two verbs cannot disagree about enrollment |
| `status file` | `<agents-root>/<id>/status` (or the crew bead) | the words the agent last said about itself, and when it wrote them |
| `pane age` | `<agents-root>/<id>/session-start` | how long this pane has been up |
| `relay` | relay control socket `/health` | is the relay up, and **which server is it bound to** |
| `relay enroll` | relay control socket `/agents` | does this relay hold a poll loop for *this* agent |
| `queue` | `<runtime>/<agent>.chan` | queued lines (unconfirmed-consumed) and the cursor a monitor restarting here would resume after |
| `delivery` | relay control socket `/delivery`, falling back to the ledger **file** | the relay's durable data-plane trail: `spooled`, `spool-failed`, `delivery-ended`, `rotated`. The socket is asked first because only a live relay can say whether recording is switched off *right now*; when it does not answer the file is read instead, and the line says so |
| `commands` | `GET /api/chat/commands` | recent invocations with state, timing, exit code, outcome |
| `last error` | whichever of the above answered | the newest failure, or an explicit "none observed (looked at: …)" |

`GET /delivery`, the ledger's four events, and their three honest limits are
[`relay.md`](relay.md)'s; `explain` adds a reader, not a truth. The on-disk
fallback reads the same two generations `parlay timeline` reads, so both verbs
see the same trail with the clock stopped; the file-then-socket resolution
lives in `explain_delivery.go`.

## The rule that governs every line

**An absence is never reported as a healthy value.** A section whose source did
not answer says `unknown` and names the source, and a section whose source did
answer says what it saw — including "there is nothing". Concretely:

- a failed `GET /api/chat/subscribers` renders `registration unknown — the
  server did not answer <url>`, never "not registered";
- when it does not answer, the roster the server **last persisted**
  (`agents.json`) is read instead, so an operator who already knows this fleet
  still sees whether the id is in it — with three limits stated on the line: the
  row is as of the last write, channel activity is **not** in that file (the
  server keeps presence in memory only), and an id that is missing from it is
  `not in the roster … not a live answer`, never the confident negative;
- a relay with no control socket says so on the `relay` line, and every
  relay-derived section separately says unknown;
- a spool that exists is not called empty, an empty spool is not called absent,
  and an unreadable one says so;
- a ledger that was never written (`exists:false`), one switched off
  (`enabled:false`), and one with no events for this agent are three different
  sentences;
- when the relay does **not** answer, the delivery trail is read off disk
  rather than declared unknowable — the ledger is a file, and its writer being
  dead does not erase it. Two things are genuinely unknowable from a file
  (whether recording is switched off now, and which server the dead relay was
  polling), and the line states that instead of implying a live relay vouched
  for the rows.

## Degraded modes, verbatim

Fixture: a private chat server, a private relay socket, a private agent home.
Nothing here touches the live fleet.

**All sources answering**

```
registration    registered — name Crew One, color #abc
channel         last observed 12m ago (2026-10-07T08:37:56Z)
crew state      working · source: status · building the parser
relay           up — polling http://127.0.0.1:60533, runtime /…/rt1
relay enroll    polling this agent
queue           2 line(s) queued in /…/rt1/crew-1.chan, unconfirmed-consumed (nothing in the fleet acknowledges a read); resume cursor m-2
delivery        3 of the last 20 ledger event(s), oldest first:
                  2026-10-07T08:37:56Z  spooled msg m-1 role=user
                  2026-10-07T08:47:56Z  SPOOL FAILED for msg m-9 — it did not reach the agent
                  2026-10-07T08:48:56Z  delivery ended — reason=channel-gone spoolLines=2
commands        2 record(s) for this agent, newest first:
                  failed   send             ended 2m ago · took 1.2s · exit 1 error
                  running  listen           started 12m ago · took 12m00s
last error      `parlay send` ended failed exit 1 outcome error (2m ago)
```

**Server down, roster on disk** — the same substitution the relay's trail gets,
for the half of the answer the server keeps in a file. Exit 0: the roster
answered. Verbatim against a private fixture with a dead server:

```
registration    listed in the roster the server last persisted to disk (/tmp/pdoc.w9fTru/state/agents.json) — name Crew One, color #abc — the server did not answer, so whether it is registered RIGHT NOW is unknown; that file holds no heartbeat either
channel         unknown — the server did not answer http://127.0.0.1:1
crew state      working · source: status-degraded · building the parser (relay unreachable; status may be stale)
status file     working [key=parser]: building the parser [last written 1s ago]
relay enroll    unknown — the relay did not answer GET /agents
```

`channel` stays `unknown` on purpose: the fallback answers one question and the
line names exactly which. Three further shapes, each pinned by a test:

- the id is **not** in that roster — `not in the roster the server last
  persisted to disk (<path>) — the server did not answer, so this is the last
  roster it wrote, not a live answer` (deliberately not `NOT in the registry`);
- there is **no** roster file — `unknown — the server did not answer <url> and
  there is no roster file at <path> to fall back on (that absence is not a 'not
  registered') — either it has never enrolled an agent or it runs with a
  -state-dir other than <statehome>`;
- a roster file that exists and **cannot be read** — `unknown — … its roster
  file at <path> exists but could not be read (<err>) — the file is there and
  what it holds is unknown, not empty`;
- the CLI targets **another machine** — `unknown — the server did not answer
  <url>, and the roster file on this host (<path>) was NOT consulted: the target
  is another machine, whose registry lives with it`. `agents.json` belongs to a
  host, so another host's file is never presented as this server's roster.
- a **live** answer always wins: with the server up, the file is not consulted
  and none of this wording appears.

**Relay down, spool exists with no writer, no ledger on disk** — exit 0

```
relay           no answer at /…/rt2/relay.sock — the relay is not running (or is using another runtime dir), so relay enrollment is unknown; the delivery ledger is a FILE and is read from disk below
relay enroll    unknown — the relay did not answer GET /agents
queue           2 line(s) queued in /…/rt2/crew-1.chan, unconfirmed-consumed (nothing in the fleet acknowledges a read); resume cursor m-2; no relay is answering, so these lines have no writer
delivery        because the relay did not answer — no ledger on disk at /…/rt2/delivery.log: this relay has never recorded a delivery event; that is NOT the same as 'nothing was delivered'
```

**Relay down, ledger on disk** — the trail the relay already wrote is still the
answer, and the substitution is named on the line. Identical run against a
private fixture, verbatim:

```
relay           no answer at /…/rt1/relay.sock — the relay is not running (or is using another runtime dir), so relay enrollment is unknown; the delivery ledger is a FILE and is read from disk below
relay enroll    unknown — the relay did not answer GET /agents
queue           no spool file at /…/rt1/crew-1.chan — nothing is queued for this agent, or the relay is not running
delivery        read from disk (/…/rt1/delivery.log) because the relay did not answer — 3 of the last 20 ledger event(s), oldest first; whether recording is switched off right now is unknown
                  2026-10-07T08:07:09Z  spooled msg m-1 role=user from=captain
                  2026-10-07T08:37:09Z  SPOOL FAILED for msg m-9 — it did not reach the agent
                  2026-10-07T08:37:09Z  delivery ended — reason=channel-gone spoolLines=2
last error      relay could not spool message m-9 — it never reached the agent (2026-10-07T08:37:09Z)
```

Four properties of that path, each pinned by a test:

- the reason names what actually happened: `because the relay did not answer` is
  printed only when no relay answered **anything**. A running relay that
  answered `/health` but did not serve `/delivery` gets `because the relay did
  not serve GET /delivery (it answered /health, so it was up; its build may
  predate the ledger, or that one request failed)` — a screen that says the
  relay is up in one row and did not answer in the next sends an operator
  looking for a dead process that is running;
- another agent's events are **not** this agent's story (the filter mirrors
  `GET /delivery?agent=`);
- the rotated generation `delivery.log.1` is read too and printed oldest-first,
  with `· N of them from the generation before the last rotation (older history
  is in <path>.1)` and the `rotated` marker row, so a shortened trail never
  reads as a quiet one;
- a ledger that exists but **cannot be opened** prints `exists but could not be
  read (…) — the file is there and what it holds is unknown, not empty` — but
  only when *nothing* was readable: if one generation refuses while the other
  answers, the rows that were read are shown and the failing generation is named
  in the coverage note (`the active ledger could not be read in full (…) so this
  may be short` / `the previous generation (…) could not be read (…), so older
  history is unknown`);
- a live relay wins: with a socket answering, the stale file is ignored and the
  disk wording never appears.

The disk path uses the ledger's own vocabulary unchanged — `spooled` is still
never printed as "delivered".

**Relay up, but this build has no delivery trail** — the relay answers
`/health` and `/agents` and 404s `/delivery`, which is what a relay built before
the ledger does. The trail on disk is still the answer, and the line says which
half of the relay was read. Verbatim against a private fixture; note the relay
line, which names the bindings this older `/health` does not carry rather than
printing `polling , runtime `:

```
relay           up — polling unknown, runtime unknown — this relay's /health reported neither, so which server it polls is UNKNOWN, not a mismatch
relay enroll    polling this agent
queue           no spool file at /…/rt6/crew-1.chan — nothing is queued for this agent, or the relay is not running
delivery        read from disk (/…/rt6/delivery.log) because the relay did not serve GET /delivery (it answered /health, so it was up; its build may predate the ledger, or that one request failed) — 3 of the last 20 ledger event(s), oldest first; whether recording is switched off right now is unknown
                  2026-10-07T08:07:09Z  spooled msg m-1 role=user from=captain
                  2026-10-07T08:37:09Z  SPOOL FAILED for msg m-9 — it did not reach the agent
                  2026-10-07T08:37:09Z  delivery ended — reason=channel-gone spoolLines=2
last error      relay could not spool message m-9 — it never reached the agent (2026-10-07T08:37:09Z)
```

**Missing resume cursor** — a spool whose lines are all non-chat events (for
example only `tts_event` rows), so a monitor restarting here would have no id to
resume after and would replay the channel. Real output of the built CLI against
a private runtime dir and agent home, with the server and relay both down (so
this is also the spool-with-no-writer case at once):

```
relay           no answer at /…/rt5/relay.sock — the relay is not running (or is using another runtime dir), so relay enrollment is unknown; the delivery ledger is a FILE and is read from disk below
relay enroll    unknown — the relay did not answer GET /agents
queue           2 line(s) queued in /…/rt5/crew-1.chan, unconfirmed-consumed (nothing in the fleet acknowledges a read); resume cursor NONE — a monitor resuming here replays this channel's backlog; no relay is answering, so these lines have no writer
delivery        because the relay did not answer — no ledger on disk at /…/rt5/delivery.log: this relay has never recorded a delivery event; that is NOT the same as 'nothing was delivered'
last error      unknown — no source that could report an error answered
```

The cursor is derived from the spool's own tail by the relay's rules (an id is
required, the role must be `user` or `agent`, and the reader keeps looking back
through the tail for the last usable line), so `NONE` is a real absence and
never a zero.

**Relay up but bound to another server** (the registered-but-deaf trap)

```
relay           up — polling http://somewhere-else:4242, runtime /…/rt3
                WARNING: this relay polls http://somewhere-else:4242, NOT the server this CLI targets (http://127.0.0.1:60533) — anything sent to http://127.0.0.1:60533 does not reach this relay
```

**Ledger never written**

```
delivery        no ledger at /…/rt4/delivery.log — this relay has never recorded a delivery event; that is NOT the same as 'nothing was delivered'
```

**Channel never observed** (blind, as distinct from drifted)

```
channel         row present, lastSeen absent — the server has never observed activity on this channel
```

An *expired* stamp instead reads `channel last observed 2.0h ago (…)`. The two
are different lines on purpose: one is an agent that went quiet, the other is an
agent the server has never heard from.

**Server unreachable** — exit 0, because the relay and the local records still
answered

```
registration    unknown — the server did not answer http://127.0.0.1:1
channel         unknown — the server did not answer http://127.0.0.1:1
crew state      working · source: status-degraded · building the parser (relay unreachable; status may be stale)
commands        unknown — the server did not answer /api/chat/commands
relay enroll    unknown — the relay did not answer GET /agents
last error      none observed (looked at: relay delivery ledger)
```

**Nothing observable at all** — the one non-zero exit

```
parlay explain: nothing was observable about crew-1 — the server did not answer at http://127.0.0.1:1 and no local relay record or status file exists
--- exit 1
```

## Exit codes

| code | meaning |
|---|---|
| 0 | at least one source answered — **including bad news**, which is the point in a half-broken fleet |
| 1 | NOTHING was observable: no server, no relay, no spool, no status, no session record |
| 2 | usage (exactly one agent id is required; an unknown flag is a hard exit, never ignored) |

## What it deliberately does not do

- No `--json`. The story is for a human at 2am; the machine-readable halves
  already exist (`parlay commands --json`, `GET /api/chat/subscribers`,
  `GET /delivery` on the relay socket) and a third schema would be one to keep
  in sync for nothing.
- It does not scan history for messages. Delivery is the relay's record, not the
  chat log's, and replaying message bodies into a diagnostic is a privacy
  regression with no diagnostic value.
- It never claims a message was **read**. Nothing in the fleet acknowledges
  consumption, so the queue line says `unconfirmed-consumed` and the ledger
  event is `spooled`, exactly as [`relay.md`](relay.md) requires.

Env: `PARLAY_SERVER`, `PARLAY_RELAY_RUNTIME`, `PARLAY_RELAY_SOCK` (the same
runtime-dir resolution `tools/monitor/parlay-monitor.sh` uses), `PARLAY_AGENT_HOME`,
`PARLAY_CREW_READ_BEADS` / `PARLAY_CREW_STORE`.
