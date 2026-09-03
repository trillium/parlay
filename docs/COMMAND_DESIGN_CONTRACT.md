# Command Design Contract

> How a command must be shaped so the Go engine can load and initiate it
> **without the command being compiled into the binary**.
>
> **Architectural shift:** today the ten builtins live in `commands.go` as a Go
> `[]commandSpec` plus a `runAction` switch — declaration AND behavior are baked
> into the binary. This contract moves the **commands** out into data and leaves
> the **machinery** in Go. Adding, editing, disabling, or reordering a command
> becomes a data edit, not a recompile.

## The boundary (what Go owns vs. what data owns)

| Go owns — **MACHINERY** (compiled, closed, rarely changes) | Data owns — **POLICY** (a manifest, changes freely) |
|---|---|
| Phrase → matcher compilation (whole/trailing, `{capture}` slots) | Which commands exist |
| Per-stream state: the submit countdown, seq, versioning | Each command's phrases, match mode, priority, description, enabled |
| The **closed action-verb vocabulary** (the only things a client can be told to do) | Which action verbs a command emits, and with what args |
| A **closed resolver registry** (named dynamic lookups: agent, page, number, channel-list) | Which resolver/transform a command's args reference |
| A **closed handler registry** (named stateful behaviors: submit) | Which handler a command delegates to, plus its static config |
| Manifest parse + validate + hot-reload | — |

**Rule of thumb:** a new command that only recombines existing verbs/resolvers is
**pure data — no rebuild**. A rebuild is required *only* to add genuinely new
machinery: a new action verb, a new resolver, or a new stateful handler. That is
the correct and minimal reason to touch Go.

## The command shape

A command is one JSON object. The manifest is an ordered list of them plus a
header.

```jsonc
{
  "schema": "parlay.commands/v1",
  "version": "2026-07-16.1",
  "commands": [
    {
      "id": "switch-tab",                     // stable unique key
      "phrases": ["switch to {agent}", "go to {agent}",
                  "show me {agent}", "channel switch {agent}"],
      "mode": "whole",                        // "whole" | "trailing" | "anywhere" | "trailing-cursor"
      "priority": 20,                          // lower wins; first match ends the pass
      "description": "Switch the active agent tab by name",
      "enabled": true,
      "emit": { /* see below */ }
    }
  ]
}
```

Every command MUST declare `id`, `phrases`, `mode`, `priority`, and `emit`.
`description` and `enabled` (default `true`) are optional.

### `emit` — two kinds

**1. `sequence`** — the declarative case. A list of actions, each a closed verb
with args that are literals or resolve/transform expressions. Covers every
stateless command.

```jsonc
"emit": {
  "kind": "sequence",
  "onResolveFail": "fallthrough",   // "fallthrough" | "noop" | "hint"
  "actions": [
    { "verb": "switchTab", "args": { "channel": { "resolve": "agent", "from": "{agent}" } } },
    { "verb": "clear" }
  ]
}
```

**2. `handler`** — the machinery case. Delegates the whole behavior to a named
server-side handler that owns its logic and any state. The manifest can only
*select and configure* a handler, never define one.

```jsonc
"emit": {
  "kind": "handler",
  "handler": "submit",              // MUST be a key in the closed handler registry
  "config": { "delayMs": 1000, "requireTail": true }
}
```

### Arg expressions (used inside `sequence` actions)

An action arg value is one of:

- **Literal** — `"main"`, `1000`, `true`.
- **Capture interpolation** — a string containing `{name}` splices the raw
  capture (`{agent}`, `{page}`). `"channel {agent}"` → `"channel mayor"`.
- **Resolve expression** — `{ "resolve": "<name>", "from": "<literal-or-{capture}>" }`
  runs a named resolver from the closed registry. On failure the action is
  skipped and `emit.onResolveFail` decides the pass outcome.
- **Transform expression** — `{ "transform": "<name>", "from": "<literal-or-{capture}>" }`
  runs a pure string transform (`slugify`, `stripTrigger`).

## Closed vocabularies (the compiled surface)

A manifest that references anything outside these is **rejected** at load
(fail-closed; the previous good manifest stays live).

### Action verbs — the only effects the engine can order
`clear` · `setText` · `submitNow` · `armTimer` · `cancelTimer` · `showHint` ·
`clearHint` · `noop` · `switchTab` · `archiveTab` · `nextTab` · `prevTab` ·
`navigate` · `stopSpeech` · `flagSpeech` · `openChannelPicker` ·
`closeChannelPicker` · `pickerHint` · `openSwitcher`

(This is exactly today's `actions.go` set. New verb ⇒ Go change ⇒ new client
dispatcher case — the intended, auditable coupling.)

### Resolvers — named dynamic lookups (take a string + request context)
| name | input | context it reads | returns |
|------|-------|------------------|---------|
| `agent` | spoken capture | `req.Tabs` | tab id (exact→substring over id/name/nickname) or ∅ |
| `page` | spoken capture | — | `/slugified/` url path |
| `number` | spoken text | `req.Tabs` | tab id by 1-based index (digits + words + ordinals) |
| `channelList` | — | `req.Tabs` | the authoritative numbered `channels[]` for `openChannelPicker` |
| `channelSelection` | spoken text | `req.Tabs` | tab id via number→name→nickname, or a cancel signal |

### Transforms — pure string functions (no context)
| name | effect |
|------|--------|
| `slugify` | lowercase, trim, spaces→`-`, wrap `/…/` |
| `stripTrigger` | remove the matched phrase from the buffer (respects `mode`), return the remainder |

### Handlers — named stateful/logic behaviors (own their state)
| name | owns |
|------|------|
| `submit` | the server-owned arm/verify/strip/submit countdown (`config.delayMs`, `config.requireTail`) |

Handlers are the escape hatch for anything a declarative `sequence` can't express
(timers, multi-step server state, irreversibility guards). They stay in Go
because their **state and reflex timing are the machinery**. The manifest chooses
and parameterizes them; it never authors them.

## Mode routing (how `channel-select` fits)

`EvalRequest.mode` selects which command set is consulted:

- `mode: ""` — normal pass over the manifest's commands by priority.
- `mode: "channel-select"` — the engine does NOT run the manifest; it invokes the
  `channelSelection` resolver directly and emits `switchTab`+`closeChannelPicker`,
  `closeChannelPicker` (cancel), or `pickerHint` (miss). Modes are machinery, not
  commands — a mode is a named entry-point into the resolver/handler registry.

New modes are a Go change (new entry-point), like new verbs. Commands within a
normal pass are data.

## Platform scoping (which surfaces a command runs on)

A **platform** is a surface the engine can drive — the Parlay chat panel, a Herdr
window, and (later) others. The same phrase can be eligible on several surfaces
while meaning a **surface-specific effect**: `change inside input` clears the
*focused input of whatever surface issued the request*, so on Herdr it clears
Herdr's input, with zero Parlay-visual coupling. The action verb stays **abstract**
(`clear` = "empty the focused input of this surface"); the platform's own
executor/dispatcher makes it concrete.

- `EvalRequest.platform` names the surface a buffer belongs to (`""` ⇒ the default,
  `parlay` — backward compatible for existing callers).
- A command declares `platforms: [...]` — the surfaces it is eligible on. Omitted ⇒
  `[defaultPlatform]` (`parlay`), so today's builtins are unchanged. A command opts
  INTO other surfaces explicitly.
- The pass only consults commands whose `platforms` include the request's platform.

```jsonc
{ "id": "clear", "phrases": ["change inside input"], "mode": "trailing",
  "priority": 10, "platforms": ["parlay", "herdr"],
  "emit": { "kind": "sequence", "actions": [ { "verb": "clear" } ] } }
```

### The closed platform registry (machinery)

Each platform declares **which verbs and handlers it implements**. The global verb
vocabulary splits into per-surface subsets:

| platform | implements |
|----------|------------|
| `parlay` | every action verb + every handler (the full visual surface) |
| `herdr`  | the text-input verbs — `clear`, `setText`, `submitNow`, `noop`, `showHint`, `clearHint` — and NO handlers yet |

Validation on every load: every platform a command targets must be registered, and
every verb/handler it emits must be in that platform's set. A `herdr`-scoped command
that emits `openChannelPicker` (a Parlay-visual verb) is **rejected at load** — the
same auditable coupling the verb registry gives us, now per-surface. A command scoped
to multiple platforms must satisfy the intersection (only emit what ALL of them
implement).

### Scoping vs. dispatch (deliberately separate)

This is the **scoping** dimension only: which commands are eligible where, and what
each surface is allowed to be told to do. **How** an action reaches a surface —
through that surface's own client/dispatcher (like Parlay's browser today) or by the
engine driving it headlessly (the autonomous, non-agent path) — is a separate
DISPATCH layer that builds on top of this and does not change the scoping model. New
platforms are a Go change (a new registry entry, like a new verb/mode); which
commands run on them is data.

## Loading, precedence, reload

The engine resolves its live command set from three layers, highest wins:

1. **Per-request override** — `EvalRequest.commands` (optional). Lets the client
   ship user-customized phrases (today's `voiceSubmitPhrases` etc. generalize to
   this) without any server state. Validated per request; invalid ⇒ ignored, fall
   through.
2. **Loaded manifest** — a file at a configured path (`PARLAY_COMMANDS` env or a
   default next to the binary), fs-watched and hot-swapped atomically on a valid
   parse.
3. **Embedded default** — a JSON `//go:embed`ed into the binary as the last-resort
   fallback, so a bare binary still works. This is the *only* place commands touch
   the binary, and it is a fallback, not the source of truth.

Validation on every load: schema/version check; every phrase compiles; every
`verb`/`resolve`/`transform`/`handler` is in the closed registry; `handler`
configs type-check. A failing manifest is rejected with a logged reason and the
prior good set stays live (never fail open to no commands).

## Proof: today's ten builtins as data

| command | emit |
|---|---|
| `clear` | `sequence`: `[clear]` |
| `flag-speech` | `sequence`: `[flagSpeech, clear]` |
| `next-tab` / `prev-tab` | `sequence`: `[nextTab, clear]` / `[prevTab, clear]` |
| `stop-speech` | `sequence`: `[stopSpeech, {setText: {transform: stripTrigger, from: buffer}}]` |
| `switch-tab` | `sequence` + `onResolveFail: fallthrough`: `[{switchTab: {resolve: agent, from: {agent}}}, clear]` |
| `archive-tab` | `sequence`: `[{archiveTab: {resolve: agent, from: {agent}}}, clear]` |
| `go-to-page` | `sequence`: `[{navigate: {transform: slugify, from: {page}}}, clear]` |
| `channel-list` | `sequence`: `[{openChannelPicker: {prompt: "...", channels: {resolve: channelList}}}, clear]` |
| `submit` | `handler: submit`, `config: {delayMs: 1000, requireTail: true}` |

All ten express cleanly; only `submit` needs a handler, and only because its
countdown is genuinely server-owned state. That is the line.

## Migration path (incremental, non-breaking)

1. Add the manifest interpreter alongside the current switch (both produce the
   same `actionList`). Embed today's builtins as the default manifest.
2. Route eval through the interpreter; delete the `runAction` switch case-by-case
   as each command's `sequence`/`handler` is proven equal (the existing
   `engine_test.go` is the equivalence oracle).
3. Externalize the embedded default to the load path + fs-watch; add per-request
   override plumbing.
4. `submit` stays a handler throughout; resolvers/transforms move from inline code
   to the named registry but keep their current logic.

The channel-picker being built now is the first validation: its `channel-list`
command must be expressible as pure `sequence` data, and its `channel-select`
behavior as a resolver — if either can't, the vocabulary is missing an entry and
that gap is the real finding.

## Open decisions (for Trillium)

1. **Where is the source of truth?** Loaded file (server-authoritative, one set
   for everyone) vs. per-request override (client/user owns their phrases). This
   contract supports both with request > file > embedded precedence — but which is
   the *primary* authoring surface shapes the UX. Recommendation: file as the
   shared default, request-override for per-user phrase customization.
2. **How far does the declarative language go?** Conditionals/branching in
   `sequence` (beyond `onResolveFail`) would let more commands avoid handlers, at
   the cost of a bigger interpreter. Recommendation: keep `sequence` flat; push
   anything needing branching into a named handler. Machinery, not a mini-language.
