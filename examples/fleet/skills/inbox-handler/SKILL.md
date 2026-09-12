---
name: inbox-handler
description: Run the persistent pi-inbox worker. Wake on coalesced pokes, atomically claim one item at a time, propagate durable knowledge, close completed items, and continue until the inbox is empty.
disable-model-invocation: false
---

# Inbox handler enrollment

You are enrolling as a ticket handler in the inbox dispatch pipeline
(`tools/inbox-dispatch/`, routed by `parlay robots-watch`). Pokes wake the
worker; the attached store remains the source of truth for available work.

## 0. Attach to a store (default: inbox)

This pipeline follows any brain federated store. The store resolves from
`STORE` (default `inbox`); channel, poke prefix, and backend derive from it
unless overridden:

| STORE | backend | channel | poke | assignee |
|---|---|---|---|---|
| `inbox` (default) | `inbox` | `pi-inbox` | `INBOX_POKE v1` | `pi-inbox` |
| `task` | `task` | `task-inbox` | `TASK_POKE v1` | `task-inbox` |
| any other | `<store>` | `<store>-inbox` | `<STORE>_POKE v1` | `<store>-inbox` |

`CHANNEL`, `POKE_PREFIX`, and `INBOX_BIN`/`STORE_BIN` override the derivation
when set. The examples below use the inbox; substitute `$STORE`'s row for any
other store. Dispatch with `STORE=<store> inbox-dispatch <id>` (or a
store-qualified id like `task-abc`, which attaches by leading token).

## 1. Enroll — one command, kept alive (one per attached store)

Run this inside your harness and keep the loop running persistently
(`Monitor({ persistent: true })`, `nohup`, or launchd):

```bash
PARLAY_SERVER=http://localhost:31337 parlay listen --agent pi-inbox --name "PI Inbox" --color "#38bdf8"
```

For another store, enroll its channel (or set `PARLAY_PI_INBOX_STORE=<store>`
and let the Pi bridge derive channel/poke/backend). In a Pi pane with the
bridge extension, prefer the arg form — it persists the store in that session:

```text
/inbox-connect sandbox
```

or raw:

```bash
PARLAY_SERVER=http://localhost:31337 parlay listen --agent task-inbox --name "Task Inbox" --color "#38bdf8"
```

Enroll BEFORE the first dispatch: `parlay send` refuses unregistered
channels, so a poke to a channel nobody has enrolled dies loudly instead of
queueing. (Proven on `sandbox-inbox`: first dispatch refused, enrollment via
`parlay listen`, re-dispatch delivered as `m186c`, listener log showed the
`SANDBOX_POKE`.)

This registers the store channel, announces, and blocks in the poll loop. The Pi
bridge turns only that store's `<STORE>_POKE v1` messages into worker wakes; ordinary chat and
close notifications are not work assignments.

Relay/server scoping matters: the relay is bound to one server URL, and
`parlay listen` preflights it — enrolling against the wrong URL fails loudly
rather than leaving you deaf. On this box the relay is scoped to
`http://localhost:31337` (which fronts the same chat state as the native
`:4242` server), hence the pin above. If preflight complains, match the URL
to your relay's scope; do not bypass the check.

Re-arming is a takeover, not an addition: one live poll loop per channel. If
the loop dies, `send` still succeeds but nobody reads — re-run `listen`
(which reaps duplicates) rather than stacking a second loop.

## 2. The serial worker loop

A message on the store channel is a `<STORE>_POKE`, not a ticket assignment. It means:

> There may be work. Inspect the attached store.

Run the worker serially until the inbox is exhausted:

1. Select the oldest eligible open item in the attached store with no zone, `zone:pi`, or
   `zone:default`; leave explicitly specialized zones alone.
2. Atomically claim it: `<store> update <id> --claim --assignee <store>-inbox`
   (`inbox update <id> --claim --assignee pi-inbox` for the inbox store).
3. Read its complete description and propagate the finding into the named
   durable project/record as a dated, source-linked learning note. Append; do
   not overwrite stale context. Do not invent adoption, productivity, or
   completion claims.
4. Close it only after the knowledge record exists:
   `<store> close <id> --reason "knowledge: <destination>; source: <id>; ..."`.
5. Recheck the store immediately and repeat until no eligible item remains.

Duplicate pokes are harmless. Never enqueue one Pi turn per poke, and never
wait for a ticket-specific reminder. A closed item is a stale delivery, not a
reason to park the shared store worker. Handoffs are for genuine blockers
or intentional handler shutdown, not normal item completion.

The close event is the completion signal. The worker remains enrolled and idle
when the store is empty, ready for the next poke.

## 3. Zones — how tickets find you

`inbox-dispatch <id> [zone]` maps zone → handler agent (attach any store via
`STORE=<store>` or a store-qualified id). With no explicit zone
it reads the ticket's `zone:<x>` label, falling back to the serial `pi` zone.
The `default` alias also resolves to `pi`; it never creates a per-ticket
`mc-<id>` inbox agent. Explicit specialized zones may still launch their own
handler agents.

The `pi` zone routes to the store channel (`pi-inbox` for inbox,
`<store>-inbox` otherwise) and never spawns a handler;
all tickets queue in the attached store for one-at-a-time handling. Filers reach you with:

```bash
<store> create "Title" -d "Description…"
<store> label add <id> zone:pi
```

(inbox: `inbox create …` / `inbox label add …`.)

The next watch pass (≤15s poll) routes `<store>:created → inbox-dispatch` →
`<STORE>_POKE` (a wake hint for you). The worker inspects and claims the next
eligible item; it does not receive a per-ticket assignment. A closed ticket
dispatches no work; a bad id fails loud and dispatches nothing.

## 4. Operating the gate

Dispatch has an independent kill switch (pausing inbox never pauses robots):

```bash
parlay inbox-dispatch off | on | status
```

When OFF the poller keeps advancing its cursor — re-enabling replays nothing.
`PARLAY_INBOX_DISPATCH=off` works without the sentinel; the sentinel wins
over `=on`.

## 5. Volume caution

The attached store is high-volume (ambient pipelines file constantly). Unzoned
and `default`-zoned creations go to the single serial store channel; they
do not spawn per-ticket agents. Explicit specialized zones can still launch
real agents, so staff those zones deliberately before leaving a persistent
`robots-watch` up — do not turn the daemon on and walk away.
