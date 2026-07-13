# Parlay Command Subsystem

The input box doubles as a command box: phrases spoken (via dictation) or typed
into it can trigger panel actions. Commands live in `src/commands/`.

## How matching works

Every input event runs one pass over the buffer (`runCommandPass`). Registered
commands are tried in `priority` order (lower first); the first match fires and
ends the pass. Every phrase gets the house dictation tolerance automatically:

- case-insensitive
- punctuation/commas allowed between the phrase's words
- interior words of ≤3 characters are optional (dictation drops them:
  "change inside in input" arrives as "change inside input")
- word-boundary guards ("exchange…" never matches a phrase starting "change…")

### Match modes

| mode | semantics | example built-in |
|---|---|---|
| `trailing` | phrase ends the buffer, content before it | `submit` ("… bravely"), `stop-speech` |
| `anywhere` | phrase anywhere → acts on the whole buffer | `clear` |
| `whole` | the buffer IS the command | `switch-tab`, `next-tab` |

### Capture slots

Phrases may contain `{slot}` tokens: `switch to {agent}`. The capture arrives in
`match.captures.agent`; validate it yourself (built-ins resolve agents against
live `agentInfo` via exact-id → exact-name → substring).

## Built-ins

| id | default phrases | mode | action |
|---|---|---|---|
| `stop-speech` | spoken pause | trailing | silence current speech, strip phrase |
| `clear` | change inside in input | anywhere | empty the whole box (+ draft hygiene) |
| `switch-tab` | switch to {agent} / go to {agent} / show me {agent} | whole | activate that agent's tab |
| `archive-tab` | archive {agent} | whole | archive that tab |
| `next-tab` / `prev-tab` | next tab / previous tab | whole | cycle tabs |
| `submit` | bravely, gravely, briefly, lap | trailing | 1s arm-and-verify, then send |

## Rebinding phrases

Settings ⚙ → the submit/clear/stop fields keep their dedicated controls; every
other command gets a generated textarea (one phrase per line) under "Voice
commands". Stored as `commandPhrases[commandId]` in the shared settings;
an empty textarea falls back to the command's shipped defaults.

## Extending (third parties / agents)

```js
window.__parlay.registerCommand({
  id: 'my-command',
  phrases: ['do the thing {target}'],
  matchMode: 'whole',
  priority: 25,
  description: 'Does the thing',
  action(ctx, m) {
    // ctx is the ONLY surface: ctx.input.{value,setText,clear,submit},
    // ctx.tabs.{list,active,switch,archive,next,prev},
    // ctx.drawer.open(), ctx.speech.stop(), ctx.settings.get()
    ctx.input.clear()
  },
})
```

Re-registering an existing `id` replaces it. A command's `watch(value, ctx,
matched)` hook runs on every pass — use it for stateful cleanup (see `submit`'s
timer). Actions must never throw across the boundary; the registry swallows
errors so a bad command can't break typing.
