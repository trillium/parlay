# Go CLI ticket B6: `robots-watch`/`robots-tail` — a TS command-*folder* becomes its own Go package

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`internal/robotswatch` ports `packages/cli/src/commands-robots-watch/{index,
detect,handlers,cursor,tail}.ts` (the panic-isolated event poll-daemon plus
its byte-offset tailer). Confirms the layout convention `internal/identity`
already established: a TS command implemented as a *folder* of files (not a
single `commands-X.ts`) gets its own Go package under `internal/`, not a file
inside `internal/commands` — `internal/commands` is reserved for the
single-file `commands-*.ts` ports (ticket B3/B4 style).

Two things worth knowing before touching this code:
- **Panic isolation is `defer`/`recover` at the same two boundaries as the TS
  try/catch**, not smeared into every I/O helper. `watch.go`'s `runPollOnce`
  recovers a whole bad pass (mirrors `index.ts`'s outer try/catch);
  `handleRoutedEvent` recovers one failing handler without losing the rest of
  that pass's diff (mirrors the per-event try/catch inside `pollOnce`).
  `tail.go`'s `tickIsolated` is the same pattern for the tailer's loop. To
  make this work, `cursor.go`'s `writeCursor` and `tail.go`'s `writeOffset`/
  `readNewLines` deliberately `panic()` on unexpected fs errors instead of
  swallowing them — matching an unguarded `mkdirSync`/`writeFileSync` throw in
  the TS source bubbling up to that same outer catch. Don't add local
  error-swallowing to those helpers; the isolation boundary belongs at the
  call sites named above, not inside the low-level I/O.
- **`detectEvents`' event order is bead-id sorted, not TS's Object.entries
  insertion order** — Go map iteration has no ordering guarantee (unlike a JS
  object's insertion-order iteration), and no test or caller depends on
  event order, so this is a deliberate, faithful-in-substance divergence, not
  a bug.

`cursor.go`'s `stateDir()` (shared by the poll cursor and the tailer's
offset file) reuses `internal/config.StateHome()` rather than reimplementing
the `PARLAY_STATE_HOME` fallback — unlike `guard.go`'s deliberately-duplicated
`guardStateHome()`, there is no TS-side inconsistency to preserve here:
`cursor.ts`'s `stateDir()` and `config.ts`'s `serverUrl()`-adjacent state-home
logic already agree.

**`commands-robots-watch/subscribe.ts` was never ported — correctly, it turns
out.** Ticket B10's TS-vs-Go test-coverage audit flagged
`subscribe.test.ts` (covering `isGuardBead`/`originatingAgent`/
`subscribeLabel`/`subscribeOnCreate`) as having no Go counterpart in
`internal/robotswatch`. Tracing it further: `subscribe.ts`'s exports are not
imported anywhere in `packages/cli/src` — not `index.ts`, not `handlers.ts`
(which implements the DELIVER/close-time half using `detect.ts`'s
`notifyChannels`, ported faithfully above) — nor anywhere else in this repo
(`grep -rl subscribeOnCreate` outside its own file/test comes up empty). It's
dead code in the TS CLI itself: pure SUBSCRIBE-on-CREATE helpers documented
as part of decision-4zr/robots-3q7n, written for some bead-creation call site
that was never wired up (in this CLI or elsewhere in the repo as of this
writing). Since it corresponds to no reachable dispatch path in either CLI,
there is nothing for `tools/cli` to port parity-wise — porting unreachable
logic just to satisfy a test-file-name checklist would be scope creep with no
behavioral referent. Left unported deliberately; if `subscribe.ts` ever gets
wired to a real call site, port it then and this note becomes obsolete.
