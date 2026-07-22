# Parlay CLI verbs + external-event triggers

> **Purpose:** two references needed to build a `robots bead created → run
> ~/.local/bin/mechanic-dispatch <ticket-id>` trigger, parlay-native.
> (1) how to author a `parlay <verb>` subcommand; (2) whether parlay has an
> event/subscribe surface an external write can fire, and if not, the
> parlay-native way to add one — generalized to ONE fabric serving both
> `robots-create → mechanic` and `request-close → notify-requester` (§2.5).
>
> **Layer boundary (decision-4zr):** beads owns EMIT (app-blind on-status-change
> hook — a beads dependency), **parlay owns SUBSCRIBE + ROUTE + DELIVER** (watch
> registry + matcher + channel/command delivery; §2.4), firstmate is the consumer.
> Contract: **subscribe → emit → route → wake.** This doc designs **parlay's half**
> to that boundary; the poll-daemon is demoted to an interim bridge for the missing
> emit.
>
> **NOT** `docs/COMMAND_DESIGN_CONTRACT.md` — that governs the Go voice/phrase
> **eval-engine** (spoken panel commands), a different subsystem entirely.
>
> Grounded 2026-07-21 against live code + the `robots`/`bd` and `projects` stores.

---

## Part 1 — Authoring a `parlay <verb>` CLI subcommand

Parlay's CLI is a **flat switch dispatcher**, not a registry/plugin system. One
file routes; each verb is a handler function in a `commands*` module. Adding a
verb is a 3–4 file edit, no framework.

### The moving parts (`packages/cli/src/`)
| File | Role |
|---|---|
| `index.ts` | The dispatcher. A single `switch (cmd)` maps a verb string → `cmdX(args)`. This is the entry (`#!/usr/bin/env bun`). |
| `commands.ts` | Home for most handlers (`cmdStatus`, `cmdSend`, `cmdMonitor`, …). One exported `async function cmd<Name>(args: string[])` per verb. |
| `commands-*.ts` / `commands-<name>/` | Split-out handlers for bigger surfaces: `commands-identity/` (say/scratchpad/identity/lifecycle), `commands-nickname.ts`, `commands-doctor.ts` (`cmdDoctor`/`cmdHealth`), `commands-variant.ts`, `commands-context-check.ts`, `commands-status.ts` (`cmdStatusVerb` — the fold §3.6 keyed status verb; see the note below), `commands-guard.ts` (`cmdGuard` + `guardRepo`/`mainWorktreePath` — the fold C4 runtime tangle+liveness backstop, also called from `commands-variant.ts`'s launch/teardown), `commands-robots-watch/` (`detect`/`cursor`/`handlers`/`index` = the §2.4 poll bridge; `tail` = the §2.4 push fast-path `robots-tail`). Same shape, just their own file when a verb grows. |
| `args.ts` | The flag parser. `parseArgs(cmd, args, boolFlags[], valueFlags[])` → `{ positionals, opts }`. Verbs declare their own bool/value flag tables (see `MEM_BOOL_FLAGS`/`MEM_VALUE_FLAGS` in `commands-identity/store.ts` for the pattern). |
| `help.ts` | `USAGE` (the top-level listing printed by `parlay help`) + per-command help strings. `helpWanted(cmd, args)` short-circuits `--help`. |
| `config.ts` | `PARLAY_SERVER` base URL + exit codes (`EXIT_USAGE=2`). |
| `http.ts` | `postJSON`/fetch helpers + `die(msg, code)` (prints to stderr, exits). |
| `format.ts`, `types.ts` | Output rendering + wire shapes. |

### The recipe (add `parlay foo`)
1. **Write the handler** in `commands.ts` (or a new `commands-foo.ts` if it's
   substantial):
   ```ts
   export async function cmdFoo(args: string[]): Promise<void> {
     if (helpWanted("foo", args)) return
     const { positionals, opts } = parseArgs("foo", args, ["--dry"], ["--zone"])
     // …do the work; hit the server via postJSON(...) or run locally…
     // on bad input: die("parlay foo: <what> required", EXIT_USAGE)
   }
   ```
2. **Register the case** in `index.ts`:
   ```ts
   import { cmdFoo } from "./commands"        // or "./commands-foo"
   // inside switch (cmd):
   case "foo": return cmdFoo(args)
   ```
3. **Wire help** in `help.ts`: add a line to `USAGE` and (optionally) a
   per-command help block keyed by `"foo"`.
4. **Test** — mirror an existing `*.test.ts` (e.g. `commands-identity.test.ts`,
   `commands-context-check.test.ts`). Bun test.

That's the whole contract. **A new verb is pure additive plumbing** — no
manifest, no codegen, no server change unless the verb calls a server endpoint.
(Precedent: `cmdDoctor`/`cmdHealth` were added this way in `commands-doctor.ts`
+ two `index.ts` cases + help.)

**Thin wrappers on PATH.** `~/.local/bin/{parlay,identity,reply,scratchpad}` are
one-line bash wrappers that `exec bun …/cli/src/index.ts <verb> "$@"` (see
`bin/parlay`). A new agent-facing verb usually wants a matching wrapper so agents
call `foo …` not `parlay foo …`.

#### The `status` verb — a name repurposed, not overloaded (fold §3.6, task-ve2v)

The parity audit (task-4bad) flagged a collision (contraction **C1**): the fold's
new agent→supervisor status verb wanted the name `status`, but `parlay status`
already existed. Resolution: the old `status` was **only a redundant fall-through
alias of bare `parlay`** — `case undefined: case "status": return cmdStatus()` —
carrying zero unique behavior. So it was **retired**, and the name rebound:

- **bare `parlay`** → the panel/fleet snapshot (`cmdStatus`, unchanged). This is
  where the snapshot lives now — full stop.
- **`parlay status <verb> [--key <slug>] <note…>`** → APPEND a keyed line to the
  agent's status stream (`cmdStatusVerb` in `commands-status.ts`).
- **`parlay status`** (bare) → READ this agent's own status file.

The emitted line uses firstmate's **exact** grammar — `<verb> [key=<slug>]: <note>`,
the key token between the verb and the colon — so `fm-classify-lib.sh`'s
`status_line_verb` / `_fm_decision_key` / `status_open_decisions` parse it with
zero changes. Verbs: `working needs-decision blocked paused done failed resolved`.
The **sink is env-configurable**: `$PARLAY_STATUS_FILE` when set (firstmate injects
it at spawn and its `fm-watch` loop reads that file), else
`~/.parlay/agents/<id>/status` keyed by `PARLAY_AGENT_ID`. One verb, two homes —
identical agent code whoever launched it.

---

## Part 2 — External-write → fire: what parlay has, and the recommendation

### 2.1 The honest answer: no built-in "external write runs a host command"
Parlay's event surfaces all deliver **to browsers or agents**, never execute a
host command:

| Surface | File | Direction | Fires what |
|---|---|---|---|
| SSE broadcast | `sse.ts` (`broadcastToClients`/`broadcastToDevice`) | server → browser/device | a client-side JS handler |
| Long-poll | `router-poll.ts` (`/api/chat/poll`) | server → agent | the agent's `monitor` loop receives a `CHAT_MSG` |
| Device command | `router-device-cmd.ts` (`POST /api/chat/device-cmd`) | server → browser | `reload` / `reset-tts` / `ping` **in the page** — NOT a shell command |
| Eval relay | `eval-relay.ts` | client ↔ Go engine | input-action SSE |

So `device-cmd` is the closest thing to "POST an event that does something," but
it does something **in the browser**, not on the host. Running
`mechanic-dispatch` (a host binary) needs a **host-side process**; it cannot be a
Parlay SSE/device event.

### 2.2 The reusable native pattern: the byte-offset JSONL tailer
Parlay *does* have a first-class pattern for **"an external process appended to a
file → parlay reacts"**: the **tailer**.

- `hook-tailer.ts` — `startHookFiringTailer()` tails
  `MEMORY/OBSERVABILITY/hook-firings.jsonl` and turns each new line into a chat
  message. No `fs.watch` — it records a **byte offset**, and on each pass reads
  only bytes past the offset (`statSync`/`openSync`/`readSync`), so a write that
  lands while nothing is watching is still caught next pass.
- `tool-tailer.ts` — same shape over `tool-activity.jsonl`.

This "persist a cursor, read only what's new, fire on each new record" loop is
**exactly** the shape a robots trigger needs — just fire `mechanic-dispatch`
instead of broadcasting a chat message. (Caveat: `startHookFiringTailer`/
`startToolEventTailer` are *defined* in the codebase but I found **no active call
site** — treat them as the reference implementation of the pattern, possibly
dormant, not as proof they run today. Verify wiring before assuming live.)

### 2.3 The source problem: robots is a Dolt store, not a JSONL
The tailer needs a file to tail. `robots` (a `bd`/Dolt store) doesn't append a
plain event JSONL by default — but two facts open a path:
- **`robots ready --json`** returns a clean array of open tickets
  (`[{id,title,description,…}]`) — a pollable current-state surface.
- **`bd set-state` "creates an event bead (source of truth)"** and **`bd hooks`**
  manages git-hook integration — so bd has a native event concept and a hook
  surface, though `bd hooks` appears git-commit-oriented and may not cover
  create-time events (unverified).

### 2.4 The architecture — layer boundary (decision-4zr)

**Ruling (decision-4zr, extends decision-3ae):** the fabric splits by layer, and
**the boundary is the answer.** Contract: **subscribe → emit → route → wake.**

| Layer | Owner | Responsibility | App-aware? |
|---|---|---|---|
| **EMIT** | **beads/bd** (dependency, NOT parlay) | a generic on-status-change hook: "bead X open→closed", durable in the store's commitments layer | **No** — app-blind, knows nothing of agents/channels |
| **SUBSCRIBE + ROUTE + DELIVER** | **parlay** (build this) | the watch registry, the matcher, and delivery to a live channel or command | **Yes** — agent/channel knowledge lives ONLY here |
| **CONSUME** | **firstmate** (and any launcher) | registers a watch, gets woken | policy, not mechanism |

Why the boundary: beads emitting *who-cares/delivery* would reach UP a layer into
agents/channels — the inverse of the robots-5cz sin — and force every store to
reinvent delivery. parlay deciding *when to emit* would couple routing to store
internals. So the store logs the event as a durable fact; **parlay delivers it.**
Same fabric as `robots→mechanic-dispatch` (store emits, parlay routes) — one
mechanism, unifying question-hx9's two consumers.

#### Parlay's half — build to this boundary
1. **SUBSCRIBE — a watch registry + `parlay watch` verb** (Part 1 authoring).
   `parlay watch add --store <s> --bead <id|pattern> --on <transition> --deliver <target>`
   persists a row to `~/.parlay/watches.json`:
   ```jsonc
   { "store":"task", "match":{"bead":"task-y9xb","transition":"open->closed"},
     "deliver":{"kind":"notify","channel":"mayor","template":"{bead} done: {close_note}"} }
   ```
   `deliver.kind` is polymorphic: **`notify`** (→ `parlay send --<channel>` / `say`,
   parlay's channel knowledge) or **`exec`** (→ run a command, e.g.
   `mechanic-dispatch {id}`). Patterns (`store:robots`, `label:zone:*`) cover the
   robots case where there's no single pre-known bead id.
2. **INGEST — consume the store-emitted event.** Parlay is already an HTTP server,
   so the clean seam is a small endpoint **`POST /api/events/bead-status`** that the
   beads emit hook calls with `{store, bead, from, to, labels, note, ts}`. (Fallback
   if the hook can't do HTTP: the emit hook appends a JSONL and parlay tails it via a
   `startBeadEventTailer()` modeled on `hook-tailer.ts` — §2.2. Either transport is
   fine; the point is parlay owns everything from ingest onward.)
3. **ROUTE — match** the event against `watches.json` (store + bead/pattern +
   transition). A closed handler registry, data-driven — new consumer = a new watch
   row, not new machinery.
4. **DELIVER — `notify`** posts to the matched channel (`parlay say --agent
   <channel>`); **`exec`** spawns the command (`Bun.spawn(["mechanic-dispatch", id])`,
   idempotent). Firstmate's monitor then wakes and reads the result — it owns nothing
   here but the watch registration and the consumption.

**The beads dependency (note, don't build):** the EMIT hook is a **beads/bd
concern** — a generic on-status-change hook that fires "bead open→closed" durably
and app-blind. Parlay's half is designed to consume whatever shape that emit takes
(HTTP post or JSONL append). *File this as a beads task; parlay's half can be built
and unit-tested against a synthetic event before the real emit exists.*

#### Interim bridge (until beads EMIT ships): the poll-daemon
Because the EMIT hook is a separate (beads) deliverable, parlay can **stand in for
the missing emit** with a poll loop that synthesizes the event.

**Shipped 2026-07-21 as `parlay robots-watch`**
(`packages/cli/src/commands-robots-watch/{detect,cursor,handlers,index}.ts`, `parlay
robots-watch --help`): polls each watched store's `<store> list --all --json --limit
0`, diffs a persisted cursor (`$PARLAY_STATE_HOME/robots-watch/cursor.json`, default
`~/.parlay/…`, mirroring the tailers' byte-offset), and feeds any detected
transition into a handler table keyed by `<store>:<kind>` (`WATCHES` in
`handlers.ts`) — the MVP's stand-in for the generic `parlay watch add` registry
described above: today a new consumer is a new row in `WATCHES` plus a `case` in
`routeEvent`, not yet a data-driven `watches.json`. Two handlers ship: robots
`created` → spawn `mechanic-dispatch <id>`; `questions`/`task` `closed` → `parlay
send --<channel>` for each `notify:<channel>` label on the bead (the label IS the
lightweight SUBSCRIBE for this MVP — no label, no subscriber, skip). First sighting
of a store seeds its cursor and fires nothing, so startup never replays history.
Both the store poll and every handler are failure-isolated (a bad store or a
failing handler logs and the pass continues) and the whole poll pass is wrapped so
one bad pass can't kill the daemon loop.

This unblocks dogfooding with zero beads change; when beads EMIT lands, the poll
source is swapped for the ingest endpoint and **subscribe/route/deliver are
unchanged**. Build the router to the event *shape*, not the poll — the poll is a
replaceable source, not the design.

**Also shipped (task-jif2): the push fast-path `parlay robots-tail`**
(`packages/cli/src/commands-robots-watch/tail.ts`, `parlay robots-tail --help`).
Rather than wait for the poll interval, it byte-offset-tails the emit stream
`$ROBOTS_EVENTS_FILE` (default `~/data/robots/events.jsonl`) — the file the
`robots create-emit` wrapper (`tools/robots-emit/robots`, installed by
`tools/robots-emit/install.sh`) appends one JSON line per created bead — and calls
`mechanic-dispatch <id>` within ~1s of the append, modeled on the server's
hook-tailer. First-ever run starts at EOF (no history replay); the persisted offset
(`$PARLAY_STATE_HOME/robots-watch/tail-offset`) resumes across restarts. This is a
concrete stand-in for the JSONL-append shape of the EMIT question in §2.6 Q1 — the
`robots create-emit` wrapper is the interim emitter, `robots-tail` the interim
consumer. **`robots-watch` (poll) stays the reconciler fallback** for any emit the
tailer missed; `mechanic-dispatch` idempotency makes a double-fire from both paths
safe. Run both under launchd.

Latency is the poll interval (seconds) for `robots-watch` — fine now; the tailer
already delivers sub-~1s, and the endpoint path is sub-second later.
`mechanic-dispatch` idempotency + the cursor make a double-fire safe.

### 2.5 One fabric, two consumers (both ride subscribe→emit→route→wake)
| Consumer | Watch (SUBSCRIBE) | Emit (beads) | Deliver (parlay) |
|---|---|---|---|
| **robots → mechanic** | `store:robots, on:created` → `exec mechanic-dispatch {id}` | new robots bead open | spawn `mechanic-dispatch {id}` (resolves zone→agent, launches via parlay-spawn) |
| **request-close → requester** | `bead:<request>, on:open->closed` → `notify {requester-channel}` | request bead closed | `parlay say --agent <requester> "{bead} done: {close_note}"` — firstmate wakes |

Both are the *same* mechanism at different `deliver.kind`s. firstmate is the
**consumer** in both: it registers the watch and gets woken; it owns no emit and no
delivery (decision-3ae — policy, not mechanism). "Closing the bead is how firstmate
learns you finished" becomes a real **event** (subscribe→emit→route→wake), not a
convention someone must remember to honor.

### 2.6 Open questions
1. **[beads dependency] What shape does the EMIT hook take?** decision-4zr puts
   EMIT in beads/bd. Does the on-status-change hook `POST` to parlay, or append a
   JSONL parlay tails? *Rec:* HTTP POST to `/api/events/bead-status` (parlay is
   already a server; push beats poll). Parlay's half consumes either — file the
   beads task, build parlay's ingest to accept both. `bd hooks` today looks
   git-commit-oriented; confirm it can fire on status-change, not just commit.
   **Interim answer (task-jif2):** the JSONL-append shape is dogfooded today — the
   `robots create-emit` wrapper appends to `~/data/robots/events.jsonl` and
   `parlay robots-tail` consumes it (see the push fast-path above). The durable
   beads-native EMIT (HTTP or JSONL) still supersedes this wrapper when it lands.
2. **[interim] Where does the poll-bridge run — launchd `com.pai.*`, or inside
   Pulse?** *Rec:* launchd for the interim bridge (independent lifecycle, survives
   Pulse restarts). The durable ingest endpoint lives in the Pulse server; the
   poll-bridge is retired once EMIT ships. **Still open** — `parlay robots-watch`
   ships as a plain long-running loop (`--interval`/`--once`); no launchd plist
   wires it up yet.
3. **Watch-registry authority + lifecycle.** Who writes `watches.json` — only
   `parlay watch` verbs, or may firstmate hand-edit? And when is a watch removed —
   one-shot (auto-drop after first delivery, right for request-close) vs standing
   (robots `on:created`)? *Rec:* `parlay watch` verbs own it; a watch carries
   `once:true|false` (request-close = once, robots = standing). **MVP answer:**
   there is no `watches.json` or `parlay watch` verb yet — `robots-watch` ships
   with the two consumers hardcoded as the `WATCHES` table in `handlers.ts` (both
   standing, no once-flag). This question is still open for whenever a third
   consumer needs a registry instead of a code change.
4. **Zone resolution (robots consumer).** `mechanic-dispatch` reads zone from a
   `zone:<x>` label, else `default`. *Rec:* the watch passes nothing; dispatch
   resolves — but that assumes filers stamp `zone:` on `robots create`; make it a
   convention or routing is always `default`.
5. **Delivery reliability.** If the target channel is offline at delivery, does the
   notify drop or queue? *Rec:* parlay already persists agent registry + history;
   a `notify` to an offline channel should land in that channel's history so the
   consumer sees it on next poll — not silently dropped.
6. **Backpressure (robots consumer).** A burst of new beads spawns several mechanics;
   `mechanic-dispatch`'s per-zone liveness check collapses same-zone concurrency to
   one launch — confirm that batching is desired before tuning cadence.
