# resumeFromSpools tombstones retired spools (task-0n80i)

`tools/relay/relay_poll.go`'s `pollLoop` already dropped a retired agent's
in-memory poll loop on the server's 410 Gone (robots-ycfa) — but only in
memory. `main.go`'s `resumeFromSpools` re-registers *every* `*.chan` file it
finds in the runtime dir at each relay startup, with no memory of which ids
were already declared dead. A long-retired agent's spool file never goes
away on its own, so each relay restart resurrected its poll loop, which then
immediately re-earned another 410 — a "watch-list drift" that only shows up
across restarts, not within one relay's uptime, and can look like continuous
410 spam if the relay is restarting often (e.g. `ensure-up.sh` misjudging a
slow-starting relay as dead — see the comment above the spool-resume call in
`main.go`).

The fix: on a terminal 410, `relay_registry.go`'s `tombstoneSpool` renames
the spool from `<agent>.chan` to `<agent>.chan.retired`. `resumeFromSpools`'s
existing `*.chan`-suffix filter then skips it automatically — no separate
blacklist to maintain. `register()` removes any leftover tombstone for an id
before creating its fresh spool, so an explicit re-registration (the normal
path a relaunched agent takes) is never blocked by a stale tombstone.

Transient errors (500, timeouts, connection refused) never tombstone — only
the explicit `errChannelGone` (410) branch does, so a live agent is never
mistaken for a retired one by this path.
