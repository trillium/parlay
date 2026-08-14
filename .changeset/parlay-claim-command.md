---
"parlay-cli": minor
---

Add **`parlay claim <task-id>`** — one-call agent bootstrap (idea-tm0). A freshly-launched agent runs it and follows the printed brief; in one command it:

- resolves its agent **profile** (id/name/color/model): flags > env (`PARLAY_AGENT_ID`/`_NAME`/`_COLOR`/`_MODEL`) > the ticket's own metadata (`parlay_agent_id`/`parlay_name`/`parlay_color`/`parlay_model`) > derived color,
- **enrolls** — registers with the chat server + announces the claim on its channel (tab goes live immediately), then prints the single `parlay listen` Monitor command to arm the persistent poll loop (`--no-register` skips the synchronous half),
- resolves the **task** — `<task-id>` against the beads/robots federation (task-/robots-/idea-…), printing the ticket's title + description as the work, so the task prompt lives on the ticket rather than the spawn prompt. Robots tickets default to a "fix + `robots close`" DoD.

`bin/parlay-spawn` gains `--claim <task-id>`: the startup prompt shrinks to a single `parlay claim <task-id>` line (the inline prompt positional becomes optional), and the tab now carries `PARLAY_AGENT_NAME`/`_COLOR`/`_MODEL` env so `claim` can resolve the profile. `mechanic-dispatch` now dispatches robots tickets via `--claim` instead of baking a task string into the spawn prompt.
