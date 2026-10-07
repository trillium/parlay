# `parlay timeline` — one ordered answer to "what happened"

**Code:** `tools/cli/internal/commands/timeline.go` (flags), `timeline_sources.go`
(every read, and what each one answers), `timeline_render.go` (the human view and
`--json`), `tools/cli/internal/timeline` (the pure merge/classify/select),
`tools/cli/internal/chathistory` (the server's own history, read off disk) and
`tools/cli/internal/relayctl/relayctl_trail.go` (the durable-file readers).

```
parlay timeline [<agent-id>] [--agent <id>|--channel <id>] [--since <when>] [--until <when>]
                [--outcome <name>[,<name>...]] [--limit <n>] [--json]
```

The 2am question this answers is **"what happened"**, as distinct from
`parlay explain`'s **"what is true right now about this one agent"**. Before it,
that question meant reading the relay's delivery ledger, the relay's audit log
and `GET /api/chat/commands` by hand, interleaving them by eye, and reading
source to know what each vocabulary meant.

It is **read-only**. It opens three files, asks one local socket, and issues one
GET. There is no write path in it to reach by mistake — pinned by a test that
asserts every control-socket request was a GET and that the trails and the
history file are byte-identical afterwards.

## Where each event comes from

| Source | Record | Answers |
|---|---|---|
| `history` | `{PARLAY_STATE_HOME}/messages.jsonl`, read **off disk** | what the **server** persisted — the only record that exists when nothing ever collected a message |
| `delivery` | `{runtime}/delivery.log` + `delivery.log.1`, read **off disk** | what the relay handed to a spool, and how each channel's delivery ended |
| `audit` | `{runtime}/audit.log` | a channel claimed, released, or a takeover refused — who/what/when |
| `command` | `GET /api/chat/commands` | an invocation's verb, state, exit code, outcome and timing |

Every record is read as a **file** except the command registry, and that is the
single most important design choice here: the operator is usually asking
*because the relay or the server is dead*, and `GET /delivery` or
`GET /api/chat/history?limit=N` cannot be asked of a dead process. The socket is
still consulted separately, for the two facts only a live relay can give —
whether recording is switched off right now (`PARLAY_RELAY_DELIVERY_LOG=0`) and
which chat server this relay polls.

The history reader keeps **five identifiers** — id, ts, channel, role, from —
and has no struct field for a message body, so a body cannot be carried into
the timeline, a store, or a log by accident. `TestReadNeverHoldsABody` pins the
field list itself, not just the current output.

## The outcome vocabulary, and its one hard rule

**Nothing in this fleet acknowledges that a message was read.** There is no
receipt, no consumer cursor the sender can see, and the relay's poll loop never
learns what the monitor tailing the spool has consumed. So there is no
`delivered` outcome, and `queued` is never upgraded into one. `--outcome
delivered` is a usage error, deliberately.

| Outcome | Meaning |
|---|---|
| `recorded` | the chat server persisted this message on this channel. **Not a delivery**: the relay's hand-over line, if there is one, is a separate event |
| `unhanded` | recorded, and the delivery trail (read in full, unrotated, and starting before this message) holds no hand-over for it: **nothing picked it up** |
| `queued` | spooled **and** the line is still in the spool — verified against the spool file, not assumed |
| `left-spool` | spooled, and the line is no longer in the spool — read or pruned, and nothing records which |
| `dropped` | the spool append **failed**: the message never reached the agent |
| `superseded` | an earlier hand-over of a message id that was handed over again later (a channel replay) |
| `ended` | the channel stopped being polled — with the reason and how many lines were still waiting |
| `rotated` | the ledger rotated here; everything older than this line is gone |
| `enrolled` / `retired` / `refused` | relay control-plane actions on a channel |
| `command` | one registry invocation: `verb → state · exit=N · outcome=T · took=D` |
| `unknown` | recorded, and this reader cannot classify it — always with the reason |

Supersession is computed in **read order** (the rotated generation first, then
the active file), because an append-only trail's read order *is* its write
order. A stamp that fails to parse therefore cannot reorder which hand-over
counts as live.

## The half the relay cannot see

The relay's trail only knows about messages the relay was handed, so before
`history` existed a message the server accepted and nothing ever collected
appeared **nowhere** — silence, where the truth was "the server has it and no
one took it". The server's own file is the other half of that story, and it is
the one record that survives the server dying.

`unhanded` is the verdict made from the two halves together, and it is only
reachable when every guard below says the absence of a hand-over **is**
evidence. Each weaker case stays `recorded` and names the guard that stopped it:

**Two questions, not one.** A hand-over line is absent for two different reasons:
the relay never took the message, or *the relay was never the delivery path for
that channel at all*. The second is not a defect — `parlay listen --legacy-poll`
polls the chat server directly with no relay involved (`docs/monitor.md`), and
what such a poll consumes leaves no spool line, no ledger line and no claim
anywhere in this fleet. The relay's own control-plane trail (`audit.log`, read
off disk in the same runtime dir) is the durable proof of which case this is: it
records `register` when a monitor claims a channel and `unregister` when the
claim is released. So the verdict needs a claim covering the message's time, and
a channel the relay never claimed is never reported as lost.

| Guard | Why the verdict cannot be made |
|---|---|
| no delivery trail at all | the ledger is a young record: a relay built before it exists has no ledger, so absence of a hand-over is absence of a *record* |
| the claim trail could not be read | whether the relay was ever the delivery path for this channel is then **unknown**, not empty — an older relay, or another runtime dir |
| the claim trail was truncated | its oldest claims are missing, so a missing claim is not evidence either |
| a claim for the channel cannot be dated | it can neither cover the message nor be ruled out |
| the relay never claimed the channel | no hand-over line was ever going to exist — a direct poll (`--legacy-poll`) leaves no record here |
| the relay's last claim on the channel ended before the message | the relay was not the delivery path any more, so its silence means nothing |
| the ledger rotated | rotation is lossy — everything older than the marker is gone |
| the ledger read was truncated | the reader's own cap means it holds a prefix, not the whole trail |
| the message is younger than the hand-over window (90s ≈ two of the relay's 45s long-polls) | the relay may simply not have polled it yet |
| the message predates the trail's own first line | the trail cannot be asked about a time it did not cover |
| the trail holds no dated line | nothing to date the window from |
| the stamp does not parse | the message cannot be placed in time at all |
| the caller supplied no evidence object | a caller that forgets to declare coverage gets no verdict, not a false one |

A hand-over that **failed** (`spool-failed`) also counts as handed: the relay
did take the message, and the `dropped` line is its verdict. Two contradictory
claims about one message id on one axis would be worse than either.

An event whose stamp does not parse keeps its row with `?` in the time column
and sorts after the dated ones. Dropping evidence because it cannot be dated
would be the same silent loss this surface exists to remove; it is also kept
inside a `--since/--until` window, for the same reason.

## Degraded modes, verbatim

Fixture: private runtime dirs, a private chat server on a refused port. Nothing
here touches a live fleet.

**A relay is not running, and the trail still answers (exit 0).** The footer
names the socket, the server and the reason each was silent — and no row is
lost:

```
parlay timeline — oldest first; 4 matching event(s)
  asked: everything the records still hold
  runtime /tmp/obsdemo/rt · server http://127.0.0.1:1

2026-10-07T06:46:49Z  4h00m ago  enrolled     crew-1            channel claimed by actor fp1
2026-10-07T07:46:49Z  3h00m ago  queued       crew-1            msg m-1 (user) from captain — still in the agent's spool — nothing in this fleet acknowledges a read, so this is queued, not delivered
2026-10-07T07:46:49Z  3h00m ago  recorded     crew-1            msg m-1 (user) from captain — the chat server persisted this message on this channel; the relay's own hand-over line for this message is in this timeline — that line, not this one, says what happened next
2026-10-07T08:46:49Z  2h00m ago  unhanded     crew-1            msg m-2 (user) from captain — the chat server persisted this message on this channel and the relay's delivery trail — read in full, with no rotation — holds no hand-over for it around the time its claim on this channel covers: NOTHING picked this message up. Either the relay polls a different chat server, or the hand-over failed without leaving a line. The message is still in the agent's history, so it can be resent

  shown: recorded=1 · unhanded=1 · queued=1 · enrolled=1

sources
  delivery ledger (read) 1 delivery event(s) read (oldest first); the relay's own rotation is recorded in the trail, so a shortened history says so · /tmp/obsdemo/rt/delivery.log
  audit log (read)       1 control-plane action(s) (register/unregister/denied) read · /tmp/obsdemo/rt/audit.log
  chat history (read)    2 message record(s) read, oldest first (id/ts/channel/role only — the reader has no field for a message body) · /tmp/obsdemo/state/messages.jsonl
  relay control socket (unreachable) no answer at /tmp/obsdemo/rt/relay.sock — the relay is not running (or uses another runtime dir). The trails above are files and were still read; only the relay's live state is unknown
  command registry (unreachable) could not ask http://127.0.0.1:1/api/chat/commands — the server did not answer (Get "http://127.0.0.1:1/api/chat/commands": dial tcp 127.0.0.1:1: connect: connection refused); commands are unknown, not absent

Nothing here is a claim that a message was READ: this fleet has no read receipt anywhere, so
`queued` means the line is still in the spool and says nothing more. `recorded` is the chat server's
own history, not a delivery; `unhanded` is the one verdict made from it, and only when the delivery
trail can be shown to be a complete record covering that message AND the relay's own claim trail
shows it was polling that channel at the time — a channel the relay never claimed, the `--legacy-poll`
path most of all, leaves no record here and is never reported as lost.
```

`--outcome unhanded` is the 2am query, and it is a first-class filter rather
than something an operator computes by eye:

```
$ parlay timeline --outcome unhanded
parlay timeline — oldest first; 1 matching event(s)
  asked: outcome unhanded

2026-10-07T08:46:49Z  2h00m ago  unhanded     crew-1            msg m-2 (user) from captain — … NOTHING picked this message up. …
```

**A channel the relay never claimed is never called lost.** This is the same
class of false accusation as the old-relay case below, and the most likely one on
a fleet that uses `--legacy-poll`: the message was consumed by a direct poll, and
that leaves no record here at all. The relay is enrolled for a *different*
channel in this fixture, so the claim trail is read and readable — the absence of
a claim for `crew-1` is positive evidence, not a failure to look:

```
2026-10-07T08:46:49Z  2h00m ago  recorded     crew-1            msg m-2 (user) from captain — the chat server persisted this message on this channel. The relay's claim trail (audit.log, read in full) holds NO claim for this channel at all — the relay was never the delivery path for it, so no hand-over line was ever going to exist. An agent can receive messages without the relay (`parlay listen --legacy-poll` polls the chat server directly), and what such a poll consumed is recorded nowhere in this fleet

$ parlay timeline --outcome unhanded
parlay timeline — oldest first; 0 event(s) matched
  no event matched. At least one record answered and held no event for this question, so the fleet really is quiet over it (see sources for what was read).
```

**The relay's claim on the channel had ended before the message.** The channel
was polled once and then released (both rows are in the timeline), so the relay
was not the delivery path when the message arrived:

```
2026-10-07T05:46:49Z  5h00m ago  enrolled     crew-1            channel claimed by actor fp1
2026-10-07T06:46:49Z  4h00m ago  retired      crew-1            channel released by actor fp1
2026-10-07T08:46:49Z  2h00m ago  recorded     crew-1            msg m-2 (user) from captain — the chat server persisted this message on this channel. The relay's claim trail (audit.log, read in full) shows the relay's last claim on this channel ended at 2026-10-07T06:46:49Z, before this message was recorded — the relay was not the delivery path for it, so a missing hand-over line is not evidence: nothing in this fleet records what has no relay behind it, including a direct poll (`parlay listen --legacy-poll`)
```

**No claim trail at all.** With no `audit.log` in the runtime dir the relay's
enrollment is **unknown**, not absent, so the verdict falls toward silence. The
`audit log (absent)` source row says which file is missing and why:

```
  audit log (absent)     no audit log — no channel has ever been claimed or released through THIS relay's control socket, so enrollment has no local record here · /tmp/obsdemo/rt/audit.log
2026-10-07T08:46:49Z  2h00m ago  recorded     crew-1            msg m-2 (user) from captain — the chat server persisted this message on this channel. The relay's claim trail (audit.log) could not be read (no audit trail at /tmp/obsdemo/rt/audit.log — this relay has never enrolled a channel here (an older relay build, or another runtime dir)), so whether the relay was ever the delivery path for this channel is unknown — a missing hand-over line is not evidence
```

**An old relay has no ledger at all — so nothing is accused.** This is the
false-positive guard that matters most, and `--outcome unhanded` matches nothing
on such a fleet:

```
2026-10-07T07:54:59Z  2h00m ago  recorded     crew-1            msg m-2 (user) from captain — the chat server persisted this message on this channel. No hand-over line for it is here and NO delivery trail could be read, so whether the relay ever took it is unknown — not absent
  delivery ledger (absent) no ledger — this relay has never recorded a delivery event. That is NOT the same as 'nothing was delivered': an older relay build has no ledger at all · /tmp/obsdemo.Dp1MUz/rt/delivery.log

$ parlay timeline --outcome unhanded
parlay timeline — oldest first; 0 event(s) matched
  no event matched. At least one record answered and held no event for this question, so the fleet really is quiet over it (see sources for what was read).
```

**The ledger rotated.** Rotation is lossy, so a missing hand-over proves nothing:

```
2026-10-07T07:54:59Z  2h00m ago  recorded     crew-1            msg m-2 (user) from captain — the chat server persisted this message on this channel. The delivery trail was read but it has rotated, so every event older than the rotation marker is gone — a missing hand-over line is not evidence
```

**The message is younger than the relay's next poll.** A five-second-old message
is not evidence of anything, and the row says so rather than guessing:

```
2026-10-07T09:55:07Z  0s ago    recorded     crew-1            msg m-9 (user) — the chat server persisted this message on this channel. It was recorded 0s ago — younger than the hand-over window (1m30s) the relay is allowed before silence means something, so this is not counted as unhanded
```

**No history file.** Named, with the reason it is not the same as "no message
was ever sent" — the server may simply run with a different `-state-dir`:

```
  chat history (absent)  no history file here — either the server has never persisted a message, or it runs with a -state-dir other than /tmp/obsdemo.Dp1MUz/state. Not the same as 'no message was ever sent' · /tmp/obsdemo.Dp1MUz/state/messages.jsonl
```

**An unreadable history file.** The file exists and what it holds is unknown:

```
  chat history (unreadable) could not read it (open /tmp/obsdemo.Dp1MUz/state/messages.jsonl: permission denied) — the file exists and what it holds is unknown · /tmp/obsdemo.Dp1MUz/state/messages.jsonl
```

**A history read that was truncated** (a 9.4 MiB file, tail-read at 8 MiB and
capped at 2000 records). Older messages are absent, and it says so instead of
looking complete:

```
  chat history (read)    2000 message record(s) read, oldest first (id/ts/channel/role only — the reader has no field for a message body) · TRUNCATED: only the newest records were read (the file is 9.4 MiB); older messages are not in this timeline · /tmp/obsdemo.Dp1MUz/state/messages.jsonl
```

**The CLI targets another host.** The history file is read off *this* host, so
when the server is elsewhere those records may not be its at all. The caveat is
only ever additive:

```
  chat history (read)    … · WARNING this is the state dir of THIS host (/tmp/obsdemo.Dp1MUz/state), and the CLI targets http://macbook:31337 — a server on another host keeps its own history there, so these records may be a different server's · /tmp/obsdemo.Dp1MUz/state/messages.jsonl
```

**A relay is not running, and the trail still answers (exit 0).** The footer
names the socket, the server and the reason each was silent — and no row is
lost:

**The ledger was never written.** "Never recorded" must not read as "nothing was
delivered", and an old relay build has no ledger at all:

```
sources
  delivery ledger (absent) no ledger — this relay has never recorded a delivery event. That is NOT the same as 'nothing was delivered': an older relay build has no ledger at all · /tmp/ptlC.8Z3IFb/delivery.log
```

**Spooled, but the spool cannot be read.** A missing spool removes the ability
to answer, so the row is `unknown` with the reason — never the friendlier
`left-spool`:

```
2026-10-07T08:35:22Z  30m01s ago  unknown      crew-1            msg m-1 (user) — the relay spooled it, but the spool could not be read (no spool at /tmp/ptlD.0Teqci/crew-1.chan — never created here, or removed; if every agent reads this way, the relay and this CLI may be using different runtime dirs), so whether the line is still waiting is not observable from here
```

**Nothing was observable at all** — exit 1, and the header says so rather than
printing an empty list that reads like a quiet fleet:

```
parlay timeline — oldest first; 0 event(s) matched
  asked: everything the records still hold

  no event matched. No record answered at all, so an empty timeline means nothing was observable — see sources.

sources
  delivery ledger (absent) no ledger — this relay has never recorded a delivery event. …
  audit log (absent)     no audit log — no channel has ever been claimed or released through THIS relay's control socket …
  relay control socket (unreachable) no answer at /tmp/ptlE.3G6kMw/relay.sock — the relay is not running …
  command registry (unreachable) could not ask http://127.0.0.1:1/api/chat/commands — the server did not answer (…

$ parlay timeline; echo $?
parlay timeline: nothing was observable — no delivery ledger, no audit log, no chat history, the relay at /tmp/ptlE.3G6kMw/relay.sock did not answer, and no command registry at http://127.0.0.1:1
1
```

**A rotation happened.** The generation before the last rotation is read too, so
the trail is shortened only where the `rotated` marker says it is:

```
2026-10-07T07:05:22Z  2h00m ago  left-spool   crew-1            msg ancient (user) — no longer in the agent's spool — read or pruned, and nothing in this fleet records which
2026-10-07T07:05:22Z  2h00m ago  rotated      -                 the ledger rotated here (size-cap) — every event older than this line is gone, so a trail that starts at this marker is not a quiet fleet
2026-10-07T08:35:22Z  30m01s ago  queued       crew-1            msg m-1 (user) — still in the agent's spool — nothing in this fleet acknowledges a read, so this is queued, not delivered

sources
  delivery ledger (read) 3 delivery event(s) read (oldest first); …
  rotated generation (read) 2 event(s) from the generation before the last rotation — read as well, so rotation shortens the trail only where the 'rotated' marker says so · /tmp/ptlF.8CHJ9Y/delivery.log.1
```

**A truncated list says it is truncated.** The header counts matches before the
limit is applied, so "newest 2 of 5" can never be mistaken for a fleet that only
did two things:

```
parlay timeline — oldest first; newest 2 of 5 matching event(s) (--limit 0 shows all)
```

**The relay polls another server.** Its trail does not describe this CLI's
traffic, so the socket line names both URLs:

```
  relay control socket (read) up — polling http://somewhere-else:4242, runtime /tmp/… · WARNING this relay polls http://somewhere-else:4242, NOT the server this CLI targets (http://127.0.0.1:60533): nothing sent to http://127.0.0.1:60533 reaches this relay
```

**The relay answers but reports no bindings.** An older relay build's `/health`
carries only its `ok` flag. The source line names that absence as UNKNOWN and
says it is not a mismatch — a warning here would be invented from silence:

```
  relay control socket (read) up — polling unknown, runtime unknown — this relay's /health reported neither, so which server it polls is UNKNOWN, not a mismatch
```

**The server is too old for a command registry.** A 404 is "there is no such
record"; a refused connection is "unknown". They are different lines because
only one of them is worth an operator's attention:

```
  command registry (unsupported)  this server answered 404 — it is older than the live-command registry, so no invocation is recorded anywhere
  command registry (unreachable)  could not ask http://127.0.0.1:1/api/chat/commands — the server did not answer (…); commands are unknown, not absent
```

**Recording is switched off.** Only the live socket can say this; the file
cannot, so an existing file is still read and the switch-off is named beside it:

```
  delivery recording (off) PARLAY_RELAY_DELIVERY_LOG=0 in the relay's environment — it is recording nothing now, so any events below predate the switch-off
```

## Time and narrowing

`--since`/`--until` accept an RFC3339 stamp or a duration back from now — `90s`,
`45m`, `2h`, `3d` (the day unit is added; `time.ParseDuration` has none). The
default read is the newest 50 events over the whole trail, oldest first;
`--limit 0` shows everything, and `--limit N` keeps the newest N **within the
filter**, not the newest N overall.

`--channel` is accepted as a name for `--agent` because in this fleet a channel
id *is* an agent id (`GET /api/chat/poll?channel=<agentId>`, `<agent>.chan`) —
naming one twice and differently is a usage error rather than a silent pick.

## What it is not

- **It is not a live view.** It reads what is recorded; `parlay commands
  --watch` and the panel are the live surfaces.
- **It does not read message bodies.** Every record it reads is
  identifier-only by design, and so is every line it prints: the history reader
  has no field for a body, so there is nothing to leak and nothing new to
  retain.
- **It does not reconcile spools beyond 64 agents in one pass.** Past that the
  per-message answer is `unknown` with a reason naming `--agent` as the way to
  get a real one.
- **It never claims a read.** See the rule above; it is restated in the footer
  of every human-readable run.
