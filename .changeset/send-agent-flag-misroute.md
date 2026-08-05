---
"@parlay/cli": patch
---

Fix `parlay send --agent <id>` silently misrouting a steer to a phantom channel, and refuse sends to unregistered agents (robots-ngg5).

`send`'s target parser treated **any** unrecognized `--flag` as the target agent id (`send --mayor "msg"` → target `mayor`). Every *other* parlay verb spells the same argument `--agent <id>` — `parlay listen --agent <id>`, the Monitor line `parlay claim` prints — so supervisors naturally typed `parlay send --agent mc-foo --from firstmate "steer"`. That parsed as target **`agent`**, with `mc-foo` folded into the message *body*: the steer landed on a channel no relay poll loop watches, the intended recipient never woke, and the caller still got `{ok:true, id:<uuid>}` back. A steer that looks delivered and isn't is the worst failure shape there is — the supervisor has no signal to retry, so the work has to be noticed missing and redispatched by hand.

Two changes, in both `tools/cli` (the live Go CLI) and `packages/cli` (the TS original), so the two can't drift:

- **`--agent <id>` and `--to <id>` are recognized explicitly** and consume the *next* token as the target. The bare `--<agent-id>` shorthand is unchanged.
- **The target is verified against the live registry (`GET /api/chat/agents`) before posting.** An unregistered id is refused with a non-zero exit and near-match suggestions instead of minting a channel nothing polls. The check **fails open**: if the registry can't be read, `send` warns on stderr and proceeds, so a transport hiccup can't become a new way to lose a message. `--force` skips it for deliberately seeding a channel before its agent registers.

Regression coverage pins both halves: `--agent`/`--to` routing to the named agent (asserting the id never leaks into the body), refusal + suggestions on an unregistered target, `--force` bypass, and the fail-open path.
