# SMS voice-flow prototype

Proves out [discussion #240](https://github.com/trillium/parlay/discussions/240)'s
step-scoped voice grammar against real iMessage/SMS data via the `imsg` CLI.
No build step, no framework — a Bun server and one static HTML+JS page.

## Run it

```sh
cd examples/sms-voice-prototype

# fake seeded data, never touches imsg or chat.db — safe anywhere
bun server.ts --demo

# real data, dry-run only (default) — reads chat.db, never sends
bun server.ts

# real data, live sending can be armed from the page's LIVE toggle
bun server.ts --allow-send
```

Then open `http://127.0.0.1:8787` (or `--port=N` to change it). The server
binds `127.0.0.1` only.

Real (non-`--demo`) mode needs **Full Disk Access** granted to your terminal
(or whatever process runs `bun`), since `imsg` reads
`~/Library/Messages/chat.db` directly. Without it, `/api/chats` and
`/api/history` will fail.

## Name resolution

`imsg chats --json` only names group chats — a 1:1 chat's `name` field is
empty, so most of the list would otherwise render as bare phone numbers, and
an unspeakable name breaks the voice grammar (the leading selector *is* the
displayed name). Since `imsg` exposes no contact lookup, `contacts.ts`
resolves bare identifiers itself, reading every
`~/Library/Application Support/AddressBook/Sources/*/AddressBook-v22.abcddb`
directly (read-only, via `bun:sqlite`) and matching phone numbers on their
last 10 digits and emails case-insensitively. The index is built once at
startup and cached in memory for the process lifetime; it never runs in
`--demo` mode. A machine with no readable Contacts data — no AddressBook
databases, or missing the permission below — logs one warning and falls back
to raw identifiers instead of failing the request. `/api/chats` keeps the raw
identifier in the payload alongside the resolved `displayName`.

Reading Contacts data this way needs **Full Disk Access** as well (the same
grant `imsg` needs) — `~/Library/Application Support/AddressBook` is inside
the same TCC-protected scope as `~/Library/Messages`.

## The grammar cheat-sheet

One utterance shape, valid in every screen:

```
utterance = [ chat-selector ] [ draft-text ] [ trailing-command ]
```

- **Leading name or stand-in word** — selects a chat, but *only* on the
  recent-messages list screen. Inside a chat, a leading name is just prose
  (say `go back` first to switch chats).
- **Middle text** — always appends to the draft. Editing is just more
  appending; `scratch that` clears it.
- **Trailing command** — `submit` (send), `scratch that` (clear draft),
  `go back` (to the list, parking the draft), `read it back` (speaks the
  draft aloud).

Canonical trace, with a contact "Joe" visible on the list:

```
"Joe bar bar"      → opens Joe's chat, draft = "bar bar"
"Baz Baz submit"   → draft = "bar bar Baz Baz", sends
```

A contact with a unique first name is speakable by first name alone ("Ana"
opens "Ana Lopez") as well as the full name. A first name shared by more than
one chat falls back to first+last for all of them; if even the full name
repeats, later occurrences fall back further to a rare stand-in word (e.g.
`penguin`), shown in parens next to the name — say the stand-in to open that
specific chat.

The pure parser lives in `grammar.js` (`parseUtterance`, `applyUtterance`,
`assignStandIns`) and is unit tested in `grammar.spec.ts` — run
`bun test` in this directory.

## The LIVE toggle

Submitting a message is **dry-run by default**. The sent screen shows the
exact `imsg send …` command it *would* run, labeled DRY RUN.

Real sending requires a double gate, both sides armed:

1. The page's **LIVE** toggle (top-right) — a human clicking it, per
   utterance.
2. The server started with **`--allow-send`**.

`--demo` mode never sends under any combination of the two — there is no
real send target for fake seeded chats.

## What's proven vs deferred

Proven end to end (see the PR body for how this was verified):

- All three UI states (list, compose, sent) with a live say-next bar
  generated from that state's command set.
- The step-scoped grammar: leading-name selector only in the list state,
  middle-text append, trailing-command stripping.
- The canonical trace, against both demo data and real `imsg` data.
- Duplicate-contact stand-in assignment and binding.
- Dry-run-by-default submit, with the double-gated live path.

Deferred (out of scope for this prototype):

- `imsg watch` / live incoming-message updates to the list.
- `imsg rpc` (JSON-RPC persistent child) — this prototype shells out per
  call, which the discussion explicitly allows.
- Real speech input beyond `webkitSpeechRecognition` when the browser
  happens to support it; no fallback ASR is bundled.
- Wiring this grammar into Parlay's actual eval-engine (`leading` phrase
  mode, per-step dynamic command registration) — this prototype only proves
  the UX/grammar shape, per the discussion's scope.
