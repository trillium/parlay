---
"parlay-cli": minor
---

**`parlay claim <task-id> --silent` claims without the monitor half (robots-nfyp).**

`parlay claim` always printed the `Monitor({ command: "parlay listen …" })` arm-command
plus the paragraph explaining how to verify that monitor survived a context compaction.
That is exactly right for a freshly-spawned harness agent, and pure noise for a scripted,
headless, or batch claim: there is no harness to arm a Monitor in, and a printed
Monitor{} call is an instruction something may act on — an unwanted watcher per bead.

`--silent` drops that half and nothing else. The profile line, the enrollment, the
folded-in identity/scratchpad memory, the task, the definition of done, and the status
protocol are all byte-identical to a default claim. The default is unchanged.

Two boundaries worth naming:

- **`--silent` is not `--no-register`.** One suppresses the printed monitor, the other
  suppresses the register/announce POSTs. They compose, and neither implies the other —
  a silent claim still puts the agent in the registry and still announces the claim.
- **It says what it costs.** An agent that reads as enrolled while nothing streams its
  channel is the registered-but-deaf failure `robots-dcag` is named after, so the brief
  states plainly that no monitor is armed and that captain messages will not arrive
  until a listener is armed separately. It states that in prose and prints no command:
  emitting the listen line you just asked to skip would defeat the flag.
