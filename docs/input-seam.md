# The input seam

**Code:** `packages/go-server/internal/inputlog` (the ledger),
`packages/go-server/internal/handlers/input_seam.go` (the three recording call
sites), `packages/go-server/internal/handlers/input_events.go` (the read
surface). Wire contract: [`api-contract.md`](api-contract.md)
§`GET /api/chat/input-events` and its machine-readable twin
[`api-contract.openapi.yaml`](api-contract.openapi.yaml).

The **input seam** is everything between "the operator said something" and "an
agent was handed it": recognition, intake validation, routing, and delivery
through either the legacy long-poll or the relay-backed spool path
([`monitor.md`](monitor.md)).

It is the least observable part of the product, and the failure mode that
motivates this ledger is that **four different failures look identical from the
panel**:

- the speech recogniser misheard the sentence;
- the relay or a listener never picked the message up;
- the command parsed but named no agent;
- the phone never sent anything at all.

The chat log cannot tell them apart. On disk, a message that was accepted and
queued is byte-identical to one that was delivered, and an input an intake
surface *refused* never reaches disk at all — the operator sees a brief red
line on a phone and nothing else, anywhere.

## The ledger

`input.jsonl` sits beside `messages.jsonl` in the state directory and holds one
`InputEvent` per **hop**, append-only, keyed by the input's id (the stored
message id, or a ledger-local `in-…` id for a refusal that stored nothing).
The in-memory ring holds the newest 5,000 events; the file is the durable
record and compacts past 8 MiB.

Three properties are load-bearing:

- **It stores no message text.** Ids, stages, classes, short reason tokens and
  numbers only. The transcript and its retention posture are unchanged; this is
  not a second copy of history.
- **A non-`ok` hop always carries a `reason`.** An input event that failed with
  no recorded reason is a defect in the tooling, not a quiet night, so
  `Validate` refuses such an event and the ledger counts it in `stats.rejected`
  rather than storing an unexplained failure.
- **Recording is out of the delivery path.** `Log.Record` only enqueues (256
  deep); one writer goroutine drains to disk. A full queue sheds and counts a
  drop, a failed append is logged and counted, and neither can propagate back
  into the request that carried the operator's message. The handler-level test
  `TestDeliveryIsNotSlowedOrFailedByAWedgedLedger` pins it: with a ledger sink
  that never returns, a send and a poll still succeed, promptly, and the
  message is still durably stored.

## The states, and what each one means

**Stages** say how far the input got. The absence of a later stage is the
answer to "where did it stop".

| Stage | Means |
|---|---|
| `received` | An intake surface accepted the input. Recorded at the phone/CLI/hook boundary. |
| `interpreted` | The intake decided what the input *is* — free text, a parsed command, or unusable speech. A recogniser error and a low-confidence transcript land here. |
| `routed` | A destination was chosen, or routing failed to choose one (`no_match`, `refused`). |
| `queued` | The input is durably held, awaiting pickup. Not a failure. |
| `delivered` | A listener was handed the input. |
| `held` | Deliberately not routed, pending an operator decision. |
| `superseded` | A later input replaced this one before it was acted on. |

**Classes** say how the hop turned out.

| Class | Means |
|---|---|
| `ok` | The hop did what it was supposed to. |
| `recogniser_error` | The recogniser reported it could not transcribe the input. There is no transcript to be unsure about. |
| `low_confidence` | A transcript exists and was reported below the threshold. Requires **both** `confidence` and `threshold`, so a hold is never unexplained. |
| `confidence_unknown` | **No confidence was reported.** Not success, not failure — "we cannot tell". This is the state every dictation lands in today, because no intake surface in this repo reports a recogniser confidence yet; a view that showed it as `ok` would be inventing evidence. |
| `no_match` | The input parsed as a command but named no destination matching a registered agent or known channel. |
| `refused` | The destination exists but delivery was refused — validation failure, stale-target refusal, unwritable store. |
| `unpicked` | The input was queued and no listener picked it up. Recorded only once something has actually waited long enough to say so. |
| `superseded` | A later input for the same destination replaced this one before it was acted on. |
| `held` | Held rather than routed, by policy. |

A view turns this into named states rather than absences: received-then-nothing
is *dropped before interpretation*, queued-with-no-delivered-hop-past-a-window
is *queued but never picked up*, and a refusal row names its reason directly.

## `source`: which door, and which delivery path

`source` names the surface or mechanism that produced the hop. Intake sources
today are `send` (`POST /api/chat/send`) and `alert` (`POST /api/chat/alert`).
Delivery is one of `poll-wake` (a parked long-poll resolved by the new message)
or `poll-backlog` (the retained store answered a cursor) — keeping those
distinct is what makes "delivered hot" tell apart from "drained after a
reconnect".

## What is recorded today, and what is not

Recorded: the `queued` hop of every operator (`role: "user"`) message accepted
by `/send` or `/alert`; the `delivered` hop at both poll delivery points; and a
`refused` hop for a `/send` the intake rejected.

**Not yet recorded, deliberately, in this unit of work:**

- `received` / `interpreted` hops for accepted input. The `queued` row already
  carries the id, stage and source, and a `received` row would be a duplicate
  keyed on the same facts until an intake surface actually has something extra
  to say at that moment (a reported confidence, a source device).
- The remote-input intake (`POST /api/chat/remote-input/submit`). It injects
  through Talon and never becomes a chat message, so its hops need their own
  keying. Its in-memory outcome map is not durable today.
- The `no_match`, `unpicked`, `held` and `superseded` classes. The vocabulary
  and the storage accept them; nothing produces them yet. `unpicked` in
  particular needs a read-time window over a queued hop, not a producer.
- A confidence or provenance threshold. The schema carries `confidence` and
  `threshold` because `low_confidence` cannot be honest without both, but no
  intake surface reports a confidence yet, so there is nothing to compare
  against and nothing is held.

A `parlay input` view and a replay-by-id verb are the intended consumers; both
read this ledger over `GET /api/chat/input-events` rather than any second
source.

## Reading it

`parlay input` renders the live view (one derived state per input, newest
first), `parlay input --input <id>` replays one input hop by hop with the gap
between hops and the hop it stopped at, and `parlay input --watch` follows new
hops as they arrive. All three are pure readers of
`GET /api/chat/input-events` and keep no state of their own, so the view and a
replay of the same id cannot disagree. `--watch` polls (default every 2s) and
says so in its header: the ledger has no push stream yet, and implying instant
delivery would be a lie about its own cadence.
