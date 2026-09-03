# Apple Notes → federated-store triage ("commander triage")

Proves out [discussion #244](https://github.com/trillium/parlay/discussions/244):
dictate a note on your Mac/phone with the trigger phrase `commander triage`
(optionally followed by a store name), and this watches Notes, routes the
note into a federated bead store, files it live, and rewrites the note with
a receipt. No build step, no framework — Bun + TypeScript, one long-running
watcher plus a one-shot pass for testing.

## Run it

```sh
cd examples/apple-notes-triage

# fake notes, fake stores, no I/O of any kind — safe anywhere
bun triage.ts --once --demo

# real Notes, one pass, only notes modified in the last 24h (or --since=<ISO>)
bun triage.ts --once

# real Notes, long-running — watches for changes and triages as they land
bun watch.ts
```

Real (non-`--demo`) mode needs:

- **Automation permission** for whatever process runs `bun` to control
  Notes (macOS will prompt the first time `osascript` addresses
  `Application("Notes")`; grant it in System Settings → Privacy & Security →
  Automation if it doesn't).
- **Full Disk Access**, if `watch.ts`'s `fs.watch` on
  `~/Library/Group Containers/group.com.apple.notes` doesn't fire — it falls
  back to a 15s mtime poll either way, so this is optional but improves
  latency.

Until you create `config.json` (see below), real mode runs against
`config.example.json` — fake stores whose `createCommand` just echoes what
it would have run. Bead creation is a genuine no-op in that state; nothing
is filed anywhere, but notes still get triaged and rewritten with a receipt
pointing at the fake store name.

## Config: your store registry

`config.example.json` (committed) lists three fake stores as a shape
reference. `config.json` (gitignored, personal — never commit it) is your
real registry and wins whenever present. Each entry:

```json
{
  "task": {
    "purpose": "Actionable work items — concrete things to do",
    "createCommand": "task create \"{title}\""
  }
}
```

- `purpose` — one line, shown to the LLM router when it has to guess a store.
- `createCommand` — shell command run via `bash -c`. `{title}` is
  substituted; the bead body (dictated text plus provenance: note id, ISO
  date) is always piped over **stdin**, never interpolated into the command
  string, so it never has to survive shell-argument escaping.
- `idPattern` (optional) — regex to pull the created bead's id out of
  stdout. Defaults to the `task`-style `store-xxxxx` shape most federated
  CLIs in this fleet already produce.

Build `config.json` from your own store list (see the federation table in
this repo's root `CLAUDE.md`) — copy `config.example.json` and swap in real
`createCommand`s for the stores you actually want notes routed into.

## The trigger phrase and routing

Say (or type) `commander triage` anywhere in a note — mid-sentence is fine,
dictation artifacts (`Commander Triage`, trailing punctuation, a stray comma
between the words) are tolerated. Routing precedence, first match wins:

1. **A store name right after the phrase** — `commander triage task` files
   into `task`, no LLM call.
2. **A bare store-name line at the top or bottom of the note** — write
   `ideas` as its own line anywhere at the start or end, say
   `commander triage` with no argument, and it routes there. This is also
   how you **answer a pending question** (see below): edit the question
   block's `answer:` line, or just add a store-name line to the note.
3. **The LLM** — an ephemeral `parlay spawn` on Claude Haiku reads the note
   and the registry's `purpose` strings and decides. Below a confidence
   threshold (0.6 default), or if it names a store outside the registry, the
   note gets a **question block** instead of being filed — edit the note to
   answer it (per #2) and it files on the next pass.

Custom phrase: `bun triage.ts --once --phrase="triage this"`.

## What gets written back to the note

Strictly bounded (discussion #244, requirement: never touch the user's own
text): the trigger phrase is stripped, and exactly one of the agent's own
delimited blocks is prepended — replacing any block it previously wrote,
never touching anything else.

Filed:

```
--- filed ---
store: task
bead: task-a1b2c
2026-09-03 14:22
-------------

<your original dictated text, untouched>
```

Needs a decision (edit `answer:` or add a store-name line, then re-trigger
or just wait for the next pass):

```
--- needs a store ---
best guesses: ideas
answer: (write a store name here, or at the top/bottom of the note)
reason: too little context to be sure
-------------------

<your original dictated text, untouched>
```

An already-filed note with no fresh `commander triage` in it is left
completely alone on later passes. Adding the trigger phrase again re-triages
it into a **new** bead, replacing the old receipt.

## Safety posture

- Every note read and write goes through `osascript` (AppleScript/JXA) —
  `notes-io.ts` never opens `NoteStore.sqlite` directly. Change detection
  watches the sqlite files for *events* only, as a wake-up signal.
- Every `osascript` call carries a hard timeout (8s default); a hung or
  contended Notes automation call is skipped for that cycle, never left to
  hang the watcher.
- Bead creation is genuinely live only once a real `config.json` exists —
  until then every store is the echo-only fake from `config.example.json`.
- The LLM tier never calls a model API directly — it's an ephemeral
  `parlay spawn --subprocess` round trip (ADR in discussion #244 #10) that
  writes one decision JSON file and exits. A non-zero exit or malformed
  result is reported as an error, never silently guessed at.
- Tests are fully hermetic (`bun test`, 53 tests) — a fake `osascript` and a
  fake store CLI shim, no real Notes, no real stores, no network.

## What's proven vs deferred

Proven end to end (see the PR body for how this was verified — generically,
per this repo's rule against real note content ever appearing in a diff or
PR):

- All routing precedences (phrase-argument, note-line, LLM), the
  confidence-threshold question path, the pending-question answer path, and
  both halves of the re-triage guard.
- The full hermetic test suite (`bun test`, 53 tests / 116 assertions).
- The real `parlay spawn --subprocess` LLM round trip against a real Haiku
  agent, with a synthetic (non-personal) note.

Deferred (out of scope for this prototype):

- Live end-to-end verification against the real Notes.app UI on this
  machine — the pure trigger/routing/block logic and the real spawn round
  trip are proven separately; wiring them together against a live note was
  left for a follow-up pass to avoid Notes-automation contention observed
  while exploring this machine's library.
- Batching multiple notes' AppleScript reads into one Apple Event (currently
  one `osascript` call per note per pass) — `listChangedNotes` already
  vectorizes the *listing* call; per-note read/write stayed one-at-a-time
  for simplicity.
- Any UI beyond editing the note itself — there's no dashboard or web view,
  by design (#244's whole point is dictate-and-forget).
