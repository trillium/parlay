# Parlay CLI verbs + external-event triggers

> **Purpose:** two references needed to build a `robots bead created → run
> ~/.local/bin/mechanic-dispatch <ticket-id>` trigger, parlay-native.
> (1) how to author a `parlay <verb>` subcommand; (2) whether parlay has an
> event/subscribe surface an external write can fire, and if not, the
> parlay-native way to add one — generalized to ONE "bead status-change → event
> → act" fabric serving both `robots-create → mechanic` and
> `request-close → notify-requester` (§2.5).
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
| `commands-*.ts` / `commands-<name>/` | Split-out handlers for bigger surfaces: `commands-identity/` (say/scratchpad/identity/lifecycle), `commands-nickname.ts`, `commands-doctor.ts` (`cmdDoctor`/`cmdHealth`), `commands-variant.ts`, `commands-context-check.ts`. Same shape, just their own file when a verb grows. |
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

### 2.4 Recommendation — poll-daemon MVP now, tailer-on-bd-event later

**MVP (build this to unblock dogfooding): a standalone poll-trigger daemon.**
A small bun script — `tools/robots-trigger/index.ts`, exposed as
`parlay robots-watch` (Part 1) and/or run under launchd (`com.pai.robots-trigger`,
alongside the other `com.pai.*` agents) — that every ~15s:
1. runs `robots ready --json`,
2. diffs ticket ids against a persisted seen-set
   (`~/.parlay/robots-trigger/seen.json` — the cursor, mirroring the tailers'
   byte-offset),
3. for each **new** open ticket, execs `mechanic-dispatch <id>` (which itself is
   idempotent: checks if the zone's mechanic is live, launches via `parlay-spawn`
   only if not).

Why this first: **zero server change, zero dependency on unverified bd internals,
robust** (a missed poll just fires next tick; the seen-set prevents double-fire;
`mechanic-dispatch` is already idempotent so even a double-fire is safe). It is
the parlay-native cursor-diff-fire loop, sourced from a CLI instead of a file. It
matches `mechanic-dispatch`'s own header note ("a daemon polling `robots ready`").
Latency is the poll interval (seconds) — fine for this flow.

**Evolution (lower latency, folds into Pulse): bd-event → JSONL → server tailer.**
If/when a `bd` post-create/`set-state` hook can append
`{ticket-id, zone}` to a `robots-events.jsonl`, add `startRobotsTailer()` in the
parlay server modeled byte-for-byte on `hook-tailer.ts`, but firing
`mechanic-dispatch` (via `Bun.spawn`) instead of `addMessage`. Sub-second latency,
one process, no polling. Gated on confirming bd can emit create events to a file
(open question below).

**Stopgap (no infra, not really an event): inline call.** The filing agent runs
`mechanic-dispatch <id>` right after `robots create`. Zero build, but couples the
trigger to whoever files and misses out-of-band / non-agent writes — a bridge,
not the durable mechanism.

**Recommended path:** ship the **poll-daemon MVP** (§2.4), keep the seen-set +
`mechanic-dispatch` idempotency as the safety net, and revisit the server-tailer
only if poll latency ever matters. It is the smallest parlay-native thing that
makes `new robots row → mechanic launched` real.

### 2.5 Generalize: ONE bead-event fabric, two consumers
The trigger must not be robots-specific. The requirement (question-hx9) is a
single primitive — **"bead status-change → event → act"** — serving both:

| Consumer | Trigger | Event → action |
|---|---|---|
| **robots → mechanic** | a robots bead is **created** (open) | run `mechanic-dispatch <id>` (launch/route the zone's mechanic) |
| **request-close → requester** | a request bead (e.g. this `question-hx9`) is **closed** | **notify its requester** — post `parlay reply`/`say` to the requester's channel so firstmate learns the answer is ready without polling |

Both are `(store, status-change, bead) → host action`. The generalization:

- **Event source.** `bd set-state` already **"creates an event bead (source of
  truth)"**, so status changes are *already* recorded as beads inside each store —
  a native, store-agnostic event stream. A create is likewise observable (a new
  open bead). Either source (event-bead stream, or a diff of `<store> list --json`
  / `ready --json`) feeds the same loop.
- **Router.** One config maps `(store, event-type[, label]) → handler`:
  - `robots` + `created` → `mechanic-dispatch {id}`
  - `questions`/`task` + `closed` + has a requester → `notify-requester {id}`
    (resolve requester from the bead's assignee/requester field → `parlay say
    --agent <requester-channel> "<bead> closed: <close-note>"`).
  New consumer = a new row, not new machinery — the same shape as adding a CLI
  verb (Part 1) or a phrase command in the voice engine: **closed handler
  registry, data-driven routing.**
- **Subscriber model.** "Requester" is the subscription. A bead names who wants to
  know; closing it emits to exactly that subscriber. This is the parlay-native
  equivalent of firstmate's status-file wake — the store *is* the message bus, the
  event bead *is* the message, `parlay say` *is* the delivery.

So the recommended poll-daemon (§2.4) should be authored **generically from day
one**: poll each watched store for status changes, diff against the seen-set, and
**dispatch through the router** — not hardcode `mechanic-dispatch`. The robots
case is the first row; request-close-notify is the second, and it's what makes
"closing the bead is how firstmate learns you finished" an *event*, not a
convention someone has to remember.

### 2.6 Open questions
1. **Can `bd`/`robots` emit create events to a file (for the tailer path)?**
   `bd set-state` creates event beads and `bd hooks` exists, but neither is
   confirmed to fire on plain `create` or to write a tailable JSONL. Needs a
   `bd hooks --help` dig before committing to the tailer evolution. *Until then,
   the poll-daemon needs none of this.*
2. **Where does the daemon run — launchd `com.pai.*`, or inside Pulse?** *Rec:*
   launchd for the MVP (independent lifecycle, survives Pulse restarts, matches
   the other PAI agents); migrate into Pulse only with the tailer path.
3. **Zone resolution.** `mechanic-dispatch` reads the zone from a `zone:<x>`
   ticket label, falling back to `default`. The trigger should pass nothing and
   let dispatch resolve — but this assumes filers stamp `zone:` labels. Worth a
   convention note (agents stamp `zone:` on `robots create`) so routing isn't
   always `default`.
4. **Poll interval + backpressure.** 15s is a guess; a burst of new tickets
   spawns several mechanics at once. `mechanic-dispatch`'s liveness check
   dedupes per-zone, so concurrent tickets in one zone collapse to one launch —
   but confirm that's the desired batching before tuning the interval.
