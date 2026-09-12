# Pi inbox bridge

`inbox create` emits a wake hint to the Parlay channel `pi-inbox`. A Pi pane
becomes the persistent worker by loading `examples/fleet/pi-inbox-bridge/pi-inbox-bridge.ts`:

```sh
./examples/fleet/pi-inbox-bridge/install.sh
```

(`examples/fleet/install.sh` installs the bridge together with the rest of the
fleet layer — inbox commands and agent skills.)

Reload Pi so it discovers the newly installed extension (for example, run
`/reload` in an existing pane). Then, in the **one pane that should handle
inbox work**, run:

```text
/inbox-connect [store]
```

A bare `/inbox-connect` attaches the inbox store (or `PARLAY_PI_INBOX_STORE`
when set); `/inbox-connect sandbox` attaches the sandbox store — channel,
poke filter, and claim/close commands derive from the named store, and the
choice persists in that session across restarts.

That command persists an opt-in marker in the Pi session and starts two children:
`parlay listen --agent pi-inbox --legacy-poll` (the channel reader) and
`parlay inbox-tail` (the enrolled watcher following the store's watch file;
`parlay robots-tail` for the robots store, and listener-only for stores with
no shipped tail). The direct poll avoids requiring
a relay in the Pi pane; listen's singleton guard still takes over any older
`pi-inbox` reader. Only `INBOX_POKE v1` messages become Pi turns. The bridge
coalesces duplicate pokes and never queues one Pi turn per inbox item. The
worker inspects the inbox store, claims one item at a time, records durable
knowledge, closes it, and repeats until exhaustion. It only claims items with
no zone, `zone:pi`, or `zone:default`; specialized zones remain separate.

The bridge is intentionally opt-in. Loading the extension in other panes does
not make them compete for the channel. `/inbox-disconnect` removes the marker
and terminates the listener and tail process groups together, so no stray
tail survives disconnect. The bridge reconnects after an
unexpected listener or tail exit while the session remains enabled.

The listener and the dispatcher must use the same `PARLAY_SERVER` value. If
the Pi pane is launched with an explicit server override, preserve it when the
extension starts `parlay`; the extension passes the pane's environment through
unchanged. The `/inbox-connect server=<url>` argument sets and persists this
value for the session. Do not run a second standalone `parlay listen
--agent pi-inbox` while the connected pane is active.

## Delivery boundary

The bridge does not claim, close, or rewrite inbox beads. It only converts a
wake hint into one worker turn. The Pi worker is responsible for atomically
claiming the next item with `inbox update <id> --claim`, propagating durable
knowledge, closing the item, and remaining alive for the next poke. A closed
item is normal completion, not a reason to park the shared worker.

## Store-attach (any federated store)

The same pipeline follows any brain store. `inbox-dispatch task-abc` attaches
by id prefix (or `STORE=task` for bare ids) and pokes the per-store channel
(`task-inbox`, `TASK_POKE v1`). Run one connected pane per store with
`PARLAY_PI_INBOX_STORE=<store>` in its environment — channel, poke filter,
and worker claim/close commands derive from it (`pi-inbox`/`INBOX_POKE v1`
remain the inbox defaults, so existing listeners survive rollout).
