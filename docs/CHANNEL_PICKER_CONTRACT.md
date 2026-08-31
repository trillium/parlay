# Channel Picker — Event/Action Pipeline Contract

> Frozen wire contract for the voice-driven channel picker. Two sides build
> against this independently: **backend** (`tools/cli/internal/evalengine`, Go) owns all
> state + logic and emits **actions**; **frontend** (`packages/client`, TS) fires
> **events** and renders its own perception of the actions it receives.
>
> Neither side may reach into the other's directory. The only coupling is the
> JSON strings below. Match them exactly.

## Principle

- **Backend holds the state.** Whether the picker is open, the ordered channel
  list, the numbering, and how a spoken utterance resolves to a channel — all
  decided server-side. There is **no agentic/LLM influence** in this loop; it is
  deterministic command matching.
- **Frontend fires events and renders.** The client sends raw input text up as
  events and paints its own full-screen modal from the data the backend hands
  back. It does not decide selection locally.

## The Loop

1. User says **"channel list"** (or `list channels` / `show channels`) into the
   main input `#pa-input`. The client already POSTs that buffer to `/eval`
   (existing flow). Now the eval request also carries `nicknames` per tab (see
   EvalRequest change) so the backend can build the list.
2. Backend matches the `channel-list` command → emits **`openChannelPicker`**
   action, whose args carry the **authoritative ordered channel list** (with the
   numbering the user will speak).
3. Frontend applies `openChannelPicker` → renders a **full-screen modal**: the
   numbered list exactly as given, an instruction line, and its **own focused
   input box** `#pa-picker-input`. Voice dictation now lands in that box.
4. User speaks a **name, nickname, or number** into the picker input. The client
   fires a **`pickerInput`** event (POST `/eval` with `mode:"channel-select"` and
   the picker text) up to the backend.
5. Backend, in `channel-select` mode, runs deterministic resolution
   (number/ordinal → name → nickname, exact then substring). On a hit it emits
   **`switchTab`** + **`closeChannelPicker`**. On a miss it emits **`pickerHint`**
   (frontend keeps the modal open and shows the hint).
6. Frontend applies `switchTab` (existing) then `closeChannelPicker` (close modal,
   return focus to `#pa-input` — skipped when the hands-free `noKeyboardMode`
   setting is on, so voice-only navigation never pops the on-screen keyboard).

An explicit **"close"** / **"cancel"** / **"never mind"** spoken into the picker
input resolves to `closeChannelPicker` with no switch.

## Wire types (JSON)

### EvalRequest (frontend → backend, existing `/eval` body — additive fields only)
```jsonc
{
  "streamId": "eval-<device>-<epoch>",   // picker input uses a DISTINCT streamId, e.g. "picker-<device>-<epoch>"
  "version":  123,
  "text":     "channel two",              // main buffer OR picker-input text
  "voiceEnabled": true,
  "mode":     "",                          // NEW. "" = normal eval; "channel-select" = resolve text as a channel pick
  "tabs": [                                // NEW: nicknames added
    { "id": "main",  "name": "Main",  "nicknames": ["main"] },
    { "id": "mayor", "name": "Mayor", "nicknames": ["mayor","boss"] }
  ]
}
```
- `mode` is set by the client based on WHICH input fired the event: main input →
  `""`; picker input → `"channel-select"`.
- `nicknames` is optional and may be empty. Backend must tolerate a missing/empty
  array.

### Actions (backend → frontend, appended to the existing action batch)

**`openChannelPicker`** — backend hands the frontend the authoritative list.
```jsonc
{ "verb": "openChannelPicker", "args": {
    "prompt": "Say a channel name, nickname, or number",
    "channels": [
      { "index": 1, "id": "main",  "label": "Main",  "nickname": "main" },
      { "index": 2, "id": "mayor", "label": "Mayor", "nickname": "boss" }
    ]
} }
```
- `index` is 1-based and is the number the user speaks.
- `label` is the display name (first nickname if present, else name). `nickname`
  is a secondary hint string (may be empty).

**`closeChannelPicker`** — dismiss the modal.
```jsonc
{ "verb": "closeChannelPicker", "args": {} }
```

**`pickerHint`** — no match; keep the modal open, show a transient hint.
```jsonc
{ "verb": "pickerHint", "args": { "text": "No channel matched \"foo\" — try again" } }
```

**`switchTab`** — UNCHANGED existing verb: `{ "verb":"switchTab", "args":{ "channel":"mayor" } }`.

## Resolution rules (backend, `channel-select` mode)

Given the spoken text (trimmed, lowercased, trailing punctuation stripped) and the
ordered `tabs` list the request carried:

1. **Number / ordinal** → `tabs[n-1]`:
   - digits: `"1"`, `"2"`, `"channel 2"`, `"number 2"`
   - words: `"one".."ten"`, ordinals `"first".."tenth"`
2. **Exact** id / name / any nickname (case-insensitive).
3. **Substring** id / name / any nickname (case-insensitive), first match wins.
4. **Cancel words**: `close`, `cancel`, `never mind`, `nevermind`, `dismiss`,
   `exit` → `closeChannelPicker`, no switch.
5. **No match** → `pickerHint`.

The number list and `tabs` order MUST be identical between what
`openChannelPicker` sends and what the picker-input request carries, so numbers
stay stable. (Client sends tabs in the same order both times.)

## Ownership

| Side | Directory | Owns |
|------|-----------|------|
| Backend | `tools/cli/internal/evalengine/` | `mode` routing, `resolveChannelSelection`, nickname matching in `resolveAgent`, the three new action constructors, the `channel-list` → `openChannelPicker` change, Go tests |
| Frontend | `packages/client/` | `#pa-picker-input` full-screen modal module, rendering `openChannelPicker.channels`, firing `pickerInput` events with `mode:"channel-select"` + a distinct streamId, `nicknames` in the tabs payload, dispatcher handling of the 3 new verbs, CSS, version bump |

Commit after each coherent piece. `git add` only files under your own directory,
by explicit path (a hook rejects `git add -A`).
