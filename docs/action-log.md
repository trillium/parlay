# The command log, and the off switch

> **What this is:** two operator surfaces over the input pipeline. The **command
> log** records one row per *evaluated string* — what it resolved to and how far
> the result actually got. The **off switch** turns a connection or an action off
> from anywhere you can read those rows. Base path is `/api/chat`, the same as
> every other route.
>
> Read [`live-commands.md`](live-commands.md) too, and read the difference
> carefully — they are two different logs with two different subjects. This one
> answers *"what did these strings do"*; that one answers *"what is parlay
> running"*.

---

## Two logs, two subjects

| | Live-command registry | **Command log** (this doc) |
|---|---|---|
| One row per | a `parlay <verb>` **process** | an **evaluated string** |
| Written by | the CLI, self-reporting | the server, at the eval relay |
| Read by | `parlay commands`, the panel's live-commands view | `parlay action-log`, the sandbox page |
| Store | `store.CommandRegistry` | `store.ActionLog` |
| Doc | [`live-commands.md`](live-commands.md) | this file |

A single keystroke in a composer produces a command-log row and no
live-command row. A single `parlay doctor` produces a live-command row and no
command-log row. Neither is a superset of the other.

---

## The outcome vocabulary

Four values. **The vocabulary is the feature** — a filter that could not tell
these apart would be decoration, and the whole point of logging an evaluation is
being able to say what happened to it.

| Outcome | Means |
|---|---|
| `delivered` | Evaluated, produced at least one action, and the resulting `input_action` reached ≥1 live SSE client for the owning device. |
| `queued` | Accepted for **later** delivery. Today that means the engine armed its own server-owned submit timer: it emitted `armTimer` and will fire `submitNow` on `/api/chat/eval-push` when the countdown elapses. |
| `dropped` | Evaluated and produced a result nobody received — **or** matched nothing at all. The `reason` says which. |
| `refused` | A gate said no, either before evaluation (the off switch) or at delivery. A refusal is an act, not an absence. |

### The `reason` token is what makes `dropped` actionable

`dropped` alone cannot distinguish a lost delivery from a string that simply
matched nothing, so every non-`delivered` row carries a short token:

| Reason | Outcome | Meaning |
|---|---|---|
| *(empty)* | `delivered` | reached a live client |
| `submit-armed` | `queued` | the engine deferred a submit to its own timer |
| `preview-suppressed` | `dropped` | a **sandbox preview** — evaluated for real, never delivered by construction |
| `no-subscriber` | `dropped` | actions were produced and nothing was listening |
| `engine-unreachable` | `dropped` | the relay could not reach the eval engine |
| `engine-bad-response` | `dropped` | the engine answered with something unparseable |
| `no-match` | `dropped` | nothing in the configuration matched — **not** a lost delivery |
| `off-connection` | `refused` | the owning device is muted |
| `off-action` | `refused` | the command that fired is muted |

The `reason` filter is a free-text field precisely because this list grows; the
`outcome` filter is a select over the closed four, and the server serves that
vocabulary in every read so no renderer hard-codes it.

---

## Reading it

```
GET /api/chat/action-log
```

| Query | Axis |
|---|---|
| `source` | `test-site` \| `panel` (which path produced it) |
| `inputAction` | the command id the string resolved **to** — the engine's `fired` |
| `outputAction` | a verb it emitted (`clear`, `setText`, `submitNow`, `armTimer`, …) |
| `outcome` | one of the four above |
| `reason` | the token from the table above |
| `device` | one connection |
| `since`, `until` | a time window — RFC3339, **or** a duration (`15m`, `2h`, `-15m`) measured back from now |
| `limit` | page size; the server caps it |

The response carries three companion fields, and they are the reason one fetch is
enough to render a working filter bar:

- `facets` — every distinct value actually present, per axis, so a renderer
  offers real choices instead of a free-text box per guessed field;
- `targets` — every connection and action currently **off**, so a row can render
  its own state;
- `outcomeVocabulary` — the closed four including values with no rows yet, so a
  filter never hides one of its own axes.

CLI: `parlay action-log` ([`../tools/cli/internal/commands/actionlog.go`](../tools/cli/internal/commands/actionlog.go)).

---

## What is never recorded

**The evaluated text.** A record holds identifiers, verb names, outcome tokens
and timings. `inputAction` is the engine's `fired` command *id*, never the
phrase that produced it. There is no text, body or phrase field on
`store.ActionRecord`, and `TestActionLogNeverStoresEvaluatedText` asserts the
serialized row does not contain the string that produced it.

This is the same rule [`live-commands.md`](live-commands.md) states for flag
values, applied to the one place it would be easiest to break: a log of "what
did this string do" is exactly where someone would be tempted to keep the string.

Field discipline matches the registry's, and the two rules differ on purpose:

- **verbs** must match `^[A-Za-z][A-Za-z0-9]*$` — an identifier the engine
  emits. Anything else is dropped **whole**, never trimmed into shape, because a
  trimmed payload still carries its first characters and would arrive looking
  like a verb.
- **identifier fields** (`source`, `device`, `streamId`, `inputAction`,
  `outcome`, `reason`) are **clamped** to `[A-Za-z0-9._-]` and a length bound,
  because an empty `device` renders as an unattributed row and an empty
  `inputAction` removes the row's meaning entirely.

---

## The off switch

```
GET  /api/chat/off-switch     what is off right now
POST /api/chat/off-switch     {"kind":"connection"|"action","id":"…","off":true|false}
```

Two targets:

- **`connection`** — a device id. Muted, that device's `/api/chat/eval` is
  refused **before the engine is called**, and any pending server-owned fire for
  its streams is refused too. The surface stops being an action source.
- **`action`** — a command id from the eval engine's manifest. Muted, that
  command's emission is suppressed after evaluation and the row is recorded as
  `refused`/`off-action`. Every other command keeps working.

### Three surfaces, one state

| Surface | Where the switch is |
|---|---|
| **Website** | every row of the sandbox page's log carries the toggle *in that row*, and the off-set is rendered above the table in the same payload the rows came from |
| **CLI** | `parlay off <connection\|action> <id>`, `parlay on …`, `parlay off status`, and `parlay action-log --off <kind>:<id>` — flippable from the same command that read the rows |
| **API** | `POST /api/chat/off-switch` |

All three read and write one in-memory set on the server, so they cannot
disagree. `GET /api/chat/off-switch` and the `targets` field of the action-log
read are the same array.

### What it is not

**It is not an authorization layer and it does not reimplement the guard.**
`/api/chat/off-switch` is an ordinary mutating route in
`internal/guard.GuardedPaths`, so the origin and content-type gates run in front
of it like every other one; a request the guard refuses never reaches the store.
The switch can only ever *subtract* delivery from work the server was already
willing to do, and it grants nothing.

### Why in-memory

Same reason `PresenceTracker` and `CommandRegistry` are: a mute that survived a
restart would be an operator intent the process cannot confirm it is still
honoring, and a silently resurrected surface is worse than an explicit re-mute.

### The residual window, stated

A submit timer armed *before* its command is muted can still fire, because the
engine armed it. The relay closes this by remembering the command each stream
last fired and re-checking it on `/api/chat/eval-push`, so the fire is refused
too. A stream whose entry has been evicted (the map is bounded at 4096, like the
stream→device map) falls back to the bound it would otherwise have: one timer's
worth, ~1s.

---

## Coverage — what this cannot see

Honest in the same way [`live-commands.md`](live-commands.md) is:

- **Only the eval relay.** A string that never reaches `/api/chat/eval` has no
  row. The remote-input intake (`/api/chat/remote-input/*`) is a different
  pipeline with its own `queued`/`injected`/`failed` vocabulary and is **not**
  folded into this log.
- **Only recent history.** The ring holds `store.DefaultActionLogMaxRecords`
  (2000) rows and sheds the oldest first. It is in-memory, so a restart starts
  empty.
- **Nothing before this log existed.** A row is written when an evaluation
  happens, not reconstructed afterwards.
- **The evaluated text**, by design — see above.

An empty result therefore means *"nothing matched this filter"*, not *"nothing
ran"*. Both renderers say so in their empty state rather than showing a blank
list to be misread.
