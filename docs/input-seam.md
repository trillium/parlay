# The input seam

**Code:** `packages/go-server/internal/inputlog` (the ledger),
`packages/go-server/internal/handlers/input_seam.go`, `input_remote.go` and
`eval.go`/`eval_supersede.go` (the recording call sites),
`packages/go-server/internal/handlers/input_events.go` (the read
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
- **A hop's `ts` is the producer's clock, not the writer's.** `Record` stamps
  the event before it is queued; the writer only formats it. The queue is
  bounded, so a burst can delay the drain, and a timestamp taken there would
  charge the observer's own backlog to the stage *before* the hop — a ledger
  hiccup reading as a slow relay. Anyone deriving latency from this ledger is
  timing the seam, not the observer
  (`TestRecordStampsAHopWhenItIsObservedNotWhenItIsWritten`).
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
| `interpreted` | The intake decided what the input *is* — free text, a parsed command, or unusable speech. A recogniser error and a low-confidence transcript land here, and so does an eval the engine read as a phrase command (below). |
| `routed` | A destination was chosen, or routing failed to choose one (`no_match`, `refused`). |
| `queued` | The input is durably held, awaiting pickup. Not a failure. |
| `delivered` | A listener was handed the input. |
| `held` | Deliberately not routed, pending an operator decision. |
| `superseded` | A later input replaced this one before it was acted on. Produced by the eval door (below). |

**Classes** say how the hop turned out.

| Class | Means |
|---|---|
| `ok` | The hop did what it was supposed to. |
| `recogniser_error` | The recogniser reported it could not transcribe the input. There is no transcript to be unsure about. Produced today by a dictation submit that arrives with empty text. |
| `low_confidence` | A transcript exists and was reported below the threshold. Requires **both** `confidence` and `threshold`, so a hold is never unexplained. |
| `confidence_unknown` | **No confidence was reported.** Not success, not failure — "we cannot tell". Every chat `/send` and `/alert` lands here, because no intake surface on those doors reports a recogniser confidence; a view that showed it as `ok` would be inventing evidence. |
| `no_match` | The input parsed as a command but named no destination that matched — a dictation submit whose focus target did not become the active app or window, a bead submit whose store has no wrapper, or an eval in `channel-select`/`sender-select` mode whose spoken name matched none of the offered channels/contacts. |
| `refused` | The destination exists but delivery was refused — validation failure, stale-target refusal, unwritable store. |
| `unpicked` | The input was queued and no listener picked it up. Recorded only once something has actually waited long enough to say so. |
| `held` | Held rather than routed, by policy. |
| `superseded` | A later input for the same destination replaced this one before it was acted on. Produced today by the eval door, from the engine's own `stale-request-version` verdict. |

A view turns this into named states rather than absences: received-then-nothing
is *dropped before interpretation*, queued-with-no-delivered-hop-past-a-window
is *queued but never picked up*, and a refusal row names its reason directly.

## `source`: which door, and which delivery path

`source` names the surface or mechanism that produced the hop. Intake sources
today are `send` (`POST /api/chat/send`), `alert` (`POST /api/chat/alert`),
`remote-input` (`POST /api/chat/remote-input/submit`, the phone dictation door)
and `eval` (`POST /api/chat/eval`, the composer's text-change up-channel that
typed and dictated text actually rides). Delivery is one of `poll-wake` (a
parked long-poll resolved by the new message) or `poll-backlog` (the retained
store answered a cursor) — keeping those distinct is what makes "delivered
hot" tell apart from "drained after a reconnect".

## The eval door: what became a command, and what matched nothing

The composer posts every text change — typed or dictated — as a versioned
buffer snapshot to `POST /api/chat/eval`, and the compiled engine answers with
actions against that snapshot. When a newer snapshot for the same stream has
already been evaluated, the engine fast-returns a `noop` whose reason is
`stale-request-version`: its own name for **a later input replaced this one
before it was acted on** (`tools/cli/internal/evalengine/engine.go`,
"Last-write-wins").

That is the sixth failure class, and it used to be completely invisible. The
relay forwarded the noop to the panel, nothing acted, and the ledger recorded
no hop at all — so "your composer had already moved on" and "the phone never
sent it" were the same silence. The relay now reads the engine's verdict out of
the action batch and records one hop:

| Field | Value |
|---|---|
| `stage` / `class` | `superseded` / `superseded` |
| `source` | `eval` |
| `reason` | `superseded-by-newer-version` |
| `detail` | `stream=<streamId> v=<version> engine=stale-request-version`, bounded to 64 runes |
| `inputId` | a ledger-local `in-…` id: the snapshot never became a message, so there is no message id to join on |

The verdict is **read, never recomputed**: the engine owns last-write-wins, and
a second version comparison in the relay could disagree with the decision that
actually produced what the panel saw. Ordinary evals record nothing at all — a
row per keystroke would drown the seam it exists to make legible — and the
detail carries the stream and version but never the text.

Because the engine has already dropped the snapshot, this class needs no
guard of its own: it is the one failure the product refuses to act on *before*
this ledger existed. What was missing was the operator being able to see that
it happened.

### What the input became

The same answer carries `fired`: the id of the command the engine decided this
buffer was, or `""` when nothing matched. That is the one question the panel
could not answer — **a phrase that matched a command and a phrase that matched
nothing leave the same visible trace** (the box keeps its text) and used to
leave the same silence in every durable record. The relay records one hop when
the field is non-empty:

| Field | Value |
|---|---|
| `stage` / `class` | `interpreted` / `ok` — the buffer *was* interpreted |
| `source` | `eval` |
| `detail` | `command=<command id> stream=<streamId> v=<version>`, both ids bounded (48 / 64 runes) because the command set can be overridden per request |
| `inputId` | a ledger-local `in-…` id |

`fired` is the engine's own verdict, quoted and never recomputed. Ordinary
evals — the ones that matched nothing and produced no hint — still record
nothing at all: a row per keystroke would drown the seam it exists to make
legible. The view calls these rows **`command`** and names the command in WHY,
and a row that stopped there must not read as an input that went nowhere, so
the derivation names it before it falls through to "stopped after
interpreted".

**`fired` is overloaded, and that overload is a trap.** In the two picker modes
the engine bypasses command matching entirely and answers with `fired` set to
the *mode name* — the resolution path that ran, not a command that matched
(`evalengine/engine.go`, the `channel-select` / `sender-select` returns). Read
as a command, that produced a healthy-looking `interpreted` / `ok` row
(`command=channel-select`) for every picker miss and, because that branch ran
first, meant the real `no_match` hop and its reason were never written at all:
the view told the operator their input had come back as a command named after
the picker. So the relay names a command only when `fired` is not the mode the
request asked for (`evalFiredNamesACommand`), and the picker's own branch below
decides the miss.

### A destination that matched nothing

`channel-select` and `sender-select` modes bypass command matching and resolve
spoken text against the channels/contacts the panel offered
(`evalengine/commands.go`, rules 1–5). Rule 5 is a miss, and the engine answers
with `pickerHint` (channels) or `senderPickerHint` (contacts) so the modal can
say "try again". The hint flashes for a second and nothing durable recorded
that it had happened: the operator said a destination, and from every record it
looked exactly like an input that was never sent. The relay records it:

| Field | Value |
|---|---|
| `stage` / `class` | `routed` / `no_match` |
| `source` | `eval` |
| `reason` | `channel-not-matched` or `sender-not-matched` — the two pickers are different failures |
| `detail` | `stream=<streamId> v=<version> mode=<mode>` plus `candidates=<n>` for `channel-select` only; the sender list is the engine's own, so claiming a count for it would be inventing evidence |

The verb is read and the hint's `args.text` is deliberately **not**: that
string contains what the operator said.

One verdict per eval is recorded, with explicit precedence: a superseded
snapshot (the engine never interpreted it) beats a fired command, which beats a
picker miss, which beats nothing. A picker mode never reaches the command
branch, because there `fired` is the mode rather than a command (above); a
picker that DID resolve records nothing, because the panel switches tab
visibly, so it is not a silent failure.

## The dictation door, and the hold

The remote-input intake is keyed by its own `ri-N` submission id, the same id
the phone polls at `GET /api/chat/remote-input/status`, so a replay names the
exact submission the operator saw. Its hops today:

| Hop | When |
|---|---|
| `received` | Every submission that reaches the handler, before it is queued. |
| `interpreted` | Only when a `confidence` was actually reported; carries the number and the threshold it was compared against. |
| `held` | A reported confidence fell below the threshold: nothing was typed, text preserved. |
| `routed` + `no_match` | The focus target did not match (nothing was typed), or a bead submit named a store it could not resolve. |
| `delivered` + `ok` | The text was typed at the target, or captured as a bead. |
| `delivered` + `refused` | The target was reached and the injection failed. |
| `interpreted` + `recogniser_error` | The submit arrived with empty text: the dictation produced nothing. Previously a bare 400 that left no trace. |
| `interpreted` + `refused` | The intake declined it before storing anything: missing `device`, unknown `mode`, invalid store, over-long bead text, or a targetless live inject. |

Reason tokens are short names (`empty-transcript`, `missing-device`,
`target-not-matched`, `inject-failed`, `bead-failed`, `no-target`, …), never the
backend's error string: a Talon or bead error can echo the text being typed, and
this ledger must never become a second copy of the transcript. The full error
stays on the submission's own status.

### The hold

A hold is the smallest honest guard against acting on input the product can
see is uncertain. The server reads a threshold from
`PARLAY_INPUT_MIN_CONFIDENCE` (a number in [0,1]; unset or empty means
**disabled**), and `inputlog.Judge` is the single owner of the decision:

| Reported confidence | Threshold | Result |
|---|---|---|
| below it | enabled | **held** — `low_confidence` + `held` hops, 202 `status: "held"`, nothing typed, text preserved |
| at or above it | enabled | routed normally |
| anything | disabled | routed normally |
| **not reported** | any | routed normally, and recorded as `confidence_unknown` rather than `ok` |

The last row is the load-bearing asymmetry: **an absent confidence is never a
hold.** A threshold that refused an unreported value would refuse every surface
that cannot report one — today, every surface — which is a policy invented on
no evidence. "Not reported" gets its own visible state instead.

The threshold travels to the reader inside `stats.minConfidence`, and
`parlay input` prints it above the table, so a hold can never be inferred from a
row without the number behind it.

## What is recorded today, and what is not

Recorded: the full hop set of the dictation intake (above); the `queued` hop of
every operator (`role: "user"`) message accepted by `/send` or `/alert`; the
`delivered` hop at both poll delivery points; a `refused` hop for a `/send` or
remote-input submit the intake rejected before storing anything; and, from the
eval door, a `superseded` hop for every snapshot the engine dropped, an
`interpreted` hop naming the command a phrase fired, and a `no_match` hop for a
picker whose spoken destination matched nothing.

**Not yet recorded, deliberately:**

- `received` / `interpreted` hops for accepted `/send` and `/alert` input. The
  `queued` row already carries the id, stage and source, and a `received` row
  would duplicate the same facts until those doors have something extra to say
  at that moment (a reported confidence, a source device).
- Successful evals that fired no command and produced no picker hint. The eval
  door records the outcomes above, not a row per keystroke: an eval that matched
  nothing carries no fact the ledger does not already hold.
- The `unpicked` class as a producer. It is a read-time judgement over a queued
  hop — nothing has to fire for "no listener picked it up in 60s" to become
  true, and the view derives it from the queued hop's age.
- A provenance threshold. The threshold today is over reported confidence
  only. No surface reports provenance strength, so a provenance hold would
  compare against a value nothing produces.

A `parlay input` view and a replay-by-id verb are the consumers; both read
this ledger over `GET /api/chat/input-events` rather than any second source.

## Reading it

`parlay input` renders the live view (one derived state per input, newest
first, plus the per-stage latency block described below), `parlay input --input
<id>` replays one input hop by hop with the gap
between hops and the hop it stopped at, and `parlay input --watch` follows new
hops as they arrive. All three are pure readers of
`GET /api/chat/input-events` and keep no state of their own, so the view and a
replay of the same id cannot disagree. `--watch` polls (default every 2s) and
says so in its header: the ledger has no push stream yet, and implying instant
delivery would be a lie about its own cadence.

The derived states are `delivered`, `queued`, `queued (unpicked)`, `refused`,
`no match`, `command` (the engine read this input as that phrase command — WHY
names which), `held`, `low confidence`, `recogniser error` and `superseded`,
plus the stage an input stopped at when no later hop was recorded. A fired
command is named *before* that last fallback, because it stopped there by
design rather than by failing to move on. The view never prints
`confidence_unknown` as a row of its own: a hop with no reported confidence is
reported by the view's confidence line ("not reported by any surface in this
window"), and folding it into the row would imply the measurement happened.

### Latency per stage

One number per input answers "how long did that one take". It cannot answer
the question an incident actually asks — *is the seam slow, and where* —
because "it felt slow" and "the recogniser took four seconds" end the same
way: the operator waited. So the snapshot prints one distribution per
hop-to-hop transition over the window it read, slowest median first, with the
end-to-end summary last:

```
LATENCY BY STAGE — hop to hop, over THIS window (nearest-rank percentiles)
  queued→delivered               1 sample(s)  p50 3.0s     p95 3.0s     max 3.0s
  received→routed                1 sample(s)  p50 <1ms     p95 <1ms     max <1ms
  received→delivered             1 sample(s)  p50 <1ms     p95 <1ms     max <1ms
  received→interpreted           1 sample(s)  p50 <1ms     p95 <1ms     max <1ms
  interpreted→held               1 sample(s)  p50 <1ms     p95 <1ms     max <1ms
  end to end (first→last hop)    4 input(s)   p50 <1ms     p95 3.0s     max 3.0s
A transition with no row is a hop that DID NOT HAPPEN — not a fast hop. received→
interpreted times this intake's own handling of an already-transcribed submission;
the recogniser runs on the phone and reports no duration in this repo.
```

The rules below are each a way a latency view lies, which is why they are
rules:

- **Percentiles are nearest-rank and always printed with their sample count.**
  With two samples a p95 *is* the max, and a percentile without `n` implies a
  distribution the window cannot support.
- **A transition with no row is a hop that did not happen, not a fast hop.** A
  still-queued input has no `queued→delivered` row because nothing was
  delivered; a one-hop input has no transition at all.
- **A pair whose timestamp does not parse, or that runs backwards, is counted
  and excluded** (`N hop pair(s) excluded`), never read as a duration of zero,
  which would make an unmeasurable hop the fastest stage in the seam.
- **End to end is timed only when every hop on the path could be timed.**
  Stitching it from a subset would measure something no input actually did.
- **An unmeasurable window says so**, in words: a block that printed nothing
  would look exactly like a seam with no delay in it.
- **The recogniser is not timed here, and the view says so.**
  `received→interpreted` measures *this intake's own handling* of an
  already-transcribed submission. Recognition runs on the phone, which reports
  a transcript and an optional confidence but no duration, and no surface in
  this repo reports one; attributing that hop to the recogniser would be the
  same defect this seam exists to remove.

The block is derived from the same hops the table is built from — no new wire
field, no server work, nothing on the delivery path. `--json` carries the same
numbers under `stageLatency` (integer milliseconds under `p50Ms`/`p95Ms`/
`maxMs`, plus `excludedHopPairs`), additive to the `events`/`stats` a script
already reads; a hop under a millisecond is `0` there and `<1ms` in the view, so
neither reads as "no time passed". The live tail prints no rolling aggregate:
`--watch` shows each hop with the gap since that same input's previous hop, and
a percentile running under a live tail is a number the operator cannot tie to
any one input.

### Who is listening: the fact the ledger cannot carry

A `queued` hop records that a message is durably held. It cannot record
whether anything will ever collect it, because a queued message a listener is
about to take and a queued message nobody is listening for are **the same
row** — so "delivery queued but never picked up" had no cause in the view at
all. The server has the cause: it sees every poll request. The same read
surface therefore reports, per channel, when anything last asked for messages
and how many long-polls are parked right now, and the view joins those facts
onto the waiting rows:

```
LISTENERS — per channel, from the server's own poll activity (runtime facts: a
restart resets them, and a channel absent here has never been polled)
  agent-a            parked pollers 1   last poll 3.0s ago  a listener is waiting on it right now
  agent-b            parked pollers 0   last poll 20m00s ago nothing is polling it
  a listener that is attached asks at least once per 25.0s (the server holds a
  parked poll that long), so a channel quiet for more than 50.0s has nothing polling it.
```

and every waiting row's WHY carries the same classification, so the answer is
where the operator is already looking:

```
queued             m4   send     agent-a     2m00s     —     no listener picked it up in 2m00s — nothing is polling this channel (last poll 20m00s ago)
queued (unpicked)  m5   send     agent-b     5m00s     —     no listener picked it up in 5m00s — no listener has asked for this channel since the server started
```

Five states, one rule (`classifyListener`, which both surfaces read so they
cannot disagree):

| State | What it means |
|---|---|
| parked | A long-poll is on this channel **right now** and the input is still queued. |
| attached | Something polled inside the hold window and left it queued. It is attached — but it is not taking this input. |
| quiet | Nothing has polled for longer than two hold windows, so nothing is listening. This is the "nobody will ever collect it" state. |
| never | The channel is absent from a report the server DID produce: nothing has polled it since the server started. |
| unreadable | A poll timestamp this view cannot parse. Not a stale one, and not a fresh one. |

The block and the rows also carry **which kind of poll was last seen**, because
attached-and-not-taking-it is a shrug until you know whether that listener
could have taken it:

```
  agent-b            parked pollers 0   last poll 4.0s ago  a listener is attached (but its last poll carried no backlog cursor)
```

`handlePoll` consults the retained store **only** for a request that carried
`after=`; every other poll parks on the live broker and so can only ever see a
message published while it waited. When the server reports that the last poll
on the channel carried no cursor *and* the input was already queued by then,
the listener in front of it was structurally unable to collect it — not slow,
not ignoring it. That is a named mechanism for the failure class the ledger
cannot hold, and it is stated only when it is provable:

| Row state + fact | WHY says |
|---|---|
| attached, cursorless poll, input queued first | `a listener is attached (last poll 4.0s ago) and has not taken it — its last poll carried no backlog cursor, so it could not have returned this input` |
| parked, cursorless poll, input queued first | the same clause after `a listener is parked on this channel and has not taken it` |
| attached, cursored poll | the plain `a listener is attached …` clause — the listener could have replayed it, so the cursor is not the reason |
| attached, cursorless poll, **input queued after the poll** | the plain clause: that poll came first, so it is not why this input is waiting |
| a server that does not report the fact | byte-identical to before |

The fact is a **three-state** field on the wire (`lastPollCursored`): a server
that reports it always sets it, and **absent** means the server does not report
it at all. Reading absent as `false` would brand every listener on an older
server as unable to replay anything — the same trap as reading an absent
`listeners` array as "nothing is listening".

Six honesty rules, each a way this could lie:

- **An absent `listeners` field is not an empty one.** The field is always
  emitted (`[]` at minimum) so an older server — which reports no listener
  activity at all — leaves every row exactly as it was and prints that
  absence. Reading the two the same way would turn an old server into a
  silently deaf fleet.
- **The window behind "nothing is polling it" is the server's own.**
  `pollHoldMs` (25s) comes over the wire and is printed; a listener that is
  attached cannot be quieter than the hold, because its next request arrives
  when the previous one returns. A hard-coded 50s in the view would be a
  guess, and would be wrong the day the hold changes.
- **A parked poller outranks every timestamp.** Something is attached right
  now, whatever the clock says.
- **An unreadable or future timestamp is neither stale nor fresh.** A server
  whose clock runs ahead must not read as "silent for hours".
- **These are runtime facts, and the block says so.** They live in memory and
  reset when the server restarts; a channel that is absent may simply have
  been quiet since that restart. The block prints that sentence rather than
  letting an operator infer history it does not have.
- **A delivered row gets no clause.** The question does not apply, and a note
  on every row would be noise that hides the rows where it matters.

The facts cost one in-memory map write on the poll path (no I/O, no error, so
no new way for a delivery to fail), and reading them never touches the
ledger's disk. `TestPollDeliveryIsNotBlockedByInputEventsReaders` proves the
constraint directly: eight goroutines hammer the read route while a parked
poll is woken by a real send, and the delivery still lands with its delivered
hop recorded.

### The live tail, and the three ways it can be incomplete

`--watch` reads **forward from a cursor** (`?afterSeq=<seq>`) rather than
re-reading a fixed newest-N window on every poll. The window form had a hole of
exactly the kind this seam exists to remove: a burst larger than the window was
printed minus whatever fell off its front, and nothing said so. A cursor cannot
be outrun, because `limit` means the **oldest** N of the set it matches when a
cursor is present (and the newest N when it is not — the snapshot view is
unchanged).

The tail can still be incomplete in two ways that no cursor fixes, so both are
printed rather than inferred — a tail that shows a gap silently is the same
defect as an input event that failed with no recorded reason:

| Line | What it means |
|---|---|
| `JOINED at the live edge (seq N) — M retained hop(s) before this tail are NOT shown` | A tail follows the live edge. The history before it is a deliberate starting point, not an omission; `parlay input` reads it. |
| `GAP — N hop(s) (seq X–Y) were evicted from the retained ledger before this tail read them` | The ring holds 5,000 events and dropped older ones between two polls. Derived from the first `seq` a page returns, because `seq` is dense: anything from `cursor+1` to it is provably missing. |
| `OBSERVER LOSS — the ledger itself did not write N record(s) (… dropped on a full queue, … rejected as malformed)` | The ledger's own writer shed records. The `stats` block travels with every page, so a tail that ignored it would be the one view able to show a hole and call it a quiet night. Only a change is announced. |
| `CURSOR AHEAD — this tail is at seq N but the ledger's newest is M (it restarted against a fresh ledger)` | `stats.newestSeq` is below the cursor: seqs began again. Without this the tail would print nothing for ever and look calm. It re-joins at the live edge. |

The view prints each INPUT id whole — including the `in-…` ids minted for
input that never became a message (a refusal, or a superseded snapshot),
which exist nowhere else, since such an id is deliberately kept off every wire
response. A truncated row would therefore be the only copy of an id it could
not be pasted back into `--input`; the column is as wide as the longest id the
ledger can mint precisely so that every row is replayable. That width is a
two-sided contract: the printer sizes its column once, and the mint keeps the
id inside it (`in-` + a 19-digit nanosecond + a two-digit suffix = 25 bytes,
pinned by `TestMintedIDsFitTheViewsColumn`).
