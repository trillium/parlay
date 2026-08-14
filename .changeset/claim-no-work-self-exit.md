---
"parlay-cli": minor
---

**A claim with no work behind it now ends the agent instead of stranding it (robots-4ek1).**

`parlay claim <task-id>` on a ticket that does not exist used to die with a bare
resolver line — `resolving "robots-aaa" via "robots" failed: no issue found` — and
nothing else. But `parlay-spawn --claim` tells a fresh agent to "follow its printed
output exactly", so that one line of stderr was the agent's entire instruction set,
and a complaint is not an instruction: the agent stayed awake, enrolled, holding a
pane, waiting for a directive that was never coming.

Both no-work shapes now take the same exit:

- **the ticket does not resolve** — there is no such work item;
- **the ticket resolves already CLOSED** — the claim is a no-op, which previously
  handed out a full work brief and sent an agent to re-do finished work.

In place of the work brief, `claim` reports the failure and prints the way out. Two
of the three steps happen on the agent's behalf, so an agent that ignores the brief
entirely still leaves a truthful record: `failed` is appended to its status file
(`crew-state`, `supervise`, and `parlay sweep`'s captain-hold all read it), and the
failure is announced on its own channel. Both are best effort and the brief only
claims credit for what actually landed — an unreachable server prints `NOT announced`
rather than telling the agent the captain has a report he never got, and it can never
suppress the printed exit procedure.

That procedure is `handoff create …` → **`identity --park <handoff-id>`**: the middle
of the three-exit model, and the only correct exit here. The brief names the other two
as wrong and says why — `--submit` reboots the agent straight back into the same dead
claim (the respawn loop this exists to prevent), and `--complete` wants an open work
item to close, which is exactly what is missing. On the closed-ticket path the finished
item is bound to the agent's identity anyway, so even an agent that reaches for
`--submit` regardless gets its reboot downgraded by `BoundWorkItemClosed`.

Exit stays `1`. `parlay claim --allow-closed <id>` is the deliberate override for
claiming a closed ticket on purpose.
