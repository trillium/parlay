---
name: inbox-handler
description: Run the persistent pi-inbox worker. Wake on coalesced pokes, atomically claim one item at a time, propagate durable knowledge, close completed items, and continue until the inbox is empty.
disable-model-invocation: false
---

# Inbox handler enrollment

You are enrolling as a ticket handler in the inbox dispatch pipeline
(`tools/inbox-dispatch/`, routed by `parlay robots-watch`). Pokes wake the
worker; the inbox store remains the source of truth for available work.

## 1. Enroll — one command, kept alive

Run this inside your harness and keep the loop running persistently
(`Monitor({ persistent: true })`, `nohup`, or launchd):

```bash
PARLAY_SERVER=http://localhost:31337 parlay listen --agent pi-inbox --name "PI Inbox" --color "#38bdf8"
```

This registers `pi-inbox`, announces, and blocks in the poll loop. The Pi
bridge turns only `INBOX_POKE v1` messages into worker wakes; ordinary chat and
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

A message on `pi-inbox` is an `INBOX_POKE`, not a ticket assignment. It means:

> There may be work. Inspect the inbox store.

Run the worker serially until the inbox is exhausted:

1. Select the oldest eligible open inbox item with no zone, `zone:pi`, or
   `zone:default`; leave explicitly specialized zones alone.
2. Atomically claim it: `inbox update <id> --claim --assignee pi-inbox`.
3. Read its complete description and propagate the finding into the named
   durable project/record as a dated, source-linked learning note. Append; do
   not overwrite stale context. Do not invent adoption, productivity, or
   completion claims.
4. Close it only after the knowledge record exists:
   `inbox close <id> --reason "knowledge: <destination>; source: <id>; ..."`.
5. Recheck the inbox immediately and repeat until no eligible item remains.

Duplicate pokes are harmless. Never enqueue one Pi turn per poke, and never
wait for a ticket-specific reminder. A closed item is a stale delivery, not a
reason to park the shared `pi-inbox` worker. Handoffs are for genuine blockers
or intentional handler shutdown, not normal item completion.

The close event is the completion signal. The worker remains enrolled and idle
when the inbox is empty, ready for the next poke.

## 3. Zones — how tickets find you

`inbox-dispatch <id> [zone]` maps zone → handler agent. With no explicit zone
it reads the ticket's `zone:<x>` label, falling back to the serial `pi` zone.
The `default` alias also resolves to `pi`; it never creates a per-ticket
`mc-<id>` inbox agent. Explicit specialized zones may still launch their own
handler agents.

The `pi` zone routes to this channel (`pi-inbox`) and never spawns a handler;
all tickets queue in the inbox store for one-at-a-time handling. Filers reach you with:

```bash
inbox create "Title" -d "Description…"
inbox label add <id> zone:pi
```

The next watch pass (≤15s poll) routes `inbox:created → inbox-dispatch` →
`INBOX_POKE` (a wake hint for you). The worker inspects and claims the next
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

The inbox store is high-volume (ambient pipelines file constantly). Unzoned
and `default`-zoned creations go to the single serial `pi-inbox` channel; they
do not spawn per-ticket agents. Explicit specialized zones can still launch
real agents, so staff those zones deliberately before leaving a persistent
`robots-watch` up — do not turn the daemon on and walk away.
