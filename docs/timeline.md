# `parlay timeline` — one ordered answer to "what happened"

**Code:** `tools/cli/internal/commands/timeline.go` (flags), `timeline_sources.go`
(every read, and what each one answers), `timeline_render.go` (the human view and
`--json`), `tools/cli/internal/timeline` (the pure merge/classify/select), and
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

It is **read-only**. It opens two files, asks one local socket, and issues one
GET. There is no write path in it to reach by mistake — pinned by a test that
asserts every control-socket request was a GET and that the trails are
byte-identical afterwards.

## Where each event comes from

| Source | Record | Answers |
|---|---|---|
| `delivery` | `{runtime}/delivery.log` + `delivery.log.1`, read **off disk** | what the relay handed to a spool, and how each channel's delivery ended |
| `audit` | `{runtime}/audit.log` | a channel claimed, released, or a takeover refused — who/what/when |
| `command` | `GET /api/chat/commands` | an invocation's verb, state, exit code, outcome and timing |

The trails are read as **files, not over the socket**, and that is the single
most important design choice here: the operator is usually asking *because the
relay is dead*, and `GET /delivery` cannot be asked of a dead process. The
socket is still consulted separately, for the two facts only a live relay can
give — whether recording is switched off right now (`PARLAY_RELAY_DELIVERY_LOG=0`)
and which chat server this relay polls.

## The outcome vocabulary, and its one hard rule

**Nothing in this fleet acknowledges that a message was read.** There is no
receipt, no consumer cursor the sender can see, and the relay's poll loop never
learns what the monitor tailing the spool has consumed. So there is no
`delivered` outcome, and `queued` is never upgraded into one. `--outcome
delivered` is a usage error, deliberately.

| Outcome | Meaning |
|---|---|
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
parlay timeline — oldest first; 6 matching event(s)
  asked: everything the records still hold
  runtime /tmp/ptlA.PzWpdi · server http://127.0.0.1:1

2026-10-07T07:05:22Z  2h00m ago  superseded   crew-1            msg m-1 (user) from captain — an earlier hand-over of this message id — it was handed over 2 time(s) and only the newest one is the live delivery (a channel replay, not a second message)
2026-10-07T07:05:22Z  2h00m ago  queued       crew-1            msg m-1 (user) from captain — still in the agent's spool — nothing in this fleet acknowledges a read, so this is queued, not delivered
2026-10-07T07:05:22Z  2h00m ago  dropped      crew-1            msg m-9 — the append to the agent's spool FAILED — this message did not reach the agent (the failure detail is in the relay's own log, not in this identifier-only trail)
2026-10-07T07:05:22Z  2h00m ago  enrolled     crew-1            channel claimed by actor a1b2c3d4
2026-10-07T08:35:22Z  30m01s ago  ended        crew-1            the channel stopped being polled — reason=channel-gone; 2 line(s) were still in the spool at that moment, unproven-consumed
2026-10-07T08:35:22Z  30m01s ago  refused      crew-1            register-denied by actor none — another caller held the channel; the attempt is recorded, never silent

  shown: queued=1 · dropped=1 · superseded=1 · ended=1 · enrolled=1 · refused=1

sources
  delivery ledger (read) 4 delivery event(s) read (oldest first); the relay's own rotation is recorded in the trail, so a shortened history says so · /tmp/ptlA.PzWpdi/delivery.log
  audit log (read)       2 control-plane action(s) (register/unregister/denied) read · /tmp/ptlA.PzWpdi/audit.log
  relay control socket (unreachable) no answer at /tmp/ptlA.PzWpdi/relay.sock — the relay is not running (or uses another runtime dir). The trails above are files and were still read; only the relay's live state is unknown
  command registry (unreachable) could not ask http://127.0.0.1:1/api/chat/commands — the server did not answer (Get "http://127.0.0.1:1/api/chat/commands": dial tcp 127.0.0.1:1: connect: connection refused); commands are unknown, not absent
```

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
parlay timeline: nothing was observable — no delivery ledger, no audit log, the relay at /tmp/ptlE.3G6kMw/relay.sock did not answer, and no command registry at http://127.0.0.1:1
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
  identifier-only by design, and so is every line it prints.
- **It does not reconcile spools beyond 64 agents in one pass.** Past that the
  per-message answer is `unknown` with a reason naming `--agent` as the way to
  get a real one.
- **It never claims a read.** See the rule above; it is restated in the footer
  of every human-readable run.
