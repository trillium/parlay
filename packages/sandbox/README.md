# parlay-sandbox

Parlay's **sandbox test site** — a react/vite page that answers two questions the
panel cannot:

1. **What would this string do, under each of my configurations?** Type a string,
   press *Preview*, and every loaded configuration is evaluated against the real
   compiled eval engine. The page shows which action each one resolves to.
2. **What has actually happened, and how do I turn it off?** The same page carries
   the **command log**, filterable by input action, output action, outcome, why,
   connection and time window — with the **off switch for a connection or an
   action built into each row**.

It is an instrument, not a product surface. There is no new endpoint, no new
protocol, and no second implementation of anything: preview and fire are the same
`POST /api/chat/eval` production uses, differing only in the stream-id marker the
server keys on.

## Running it

The page must reach the server **same-origin**, because the chat API's origin
guard is the boundary and the sandbox must not weaken it. Two ways, both of which
keep the page inside that guard without adding an allowed origin:

```sh
# 1. Against a running parlay server, via Vite's dev proxy.
#    The proxy forwards the page's own Origin and Host unchanged, so the
#    server's same-origin rule accepts it — no PARLAY_ALLOWED_ORIGINS needed.
cd packages/sandbox
PARLAY_SERVER=http://127.0.0.1:4242 bun run dev      # http://localhost:5173

# 2. Built and served by the server itself (the go-server's static mount).
bun run build                                        # → packages/sandbox/dist
```

The eval engine is required for *Preview* to say anything useful — the engine is
what decides. Start one with `parlay eval serve` (see
[`docs/action-log.md`](../../docs/action-log.md) and `parlay eval --help`).

## Preview vs fire — this is the whole design

| | Preview | Fire |
|---|---|---|
| Trigger | the *Preview* button | the *Fire* button, **per configuration row** |
| Stream id | `sandbox-preview-<config>-<n>` | `sandbox-fire-<config>-<n>` |
| Route | `POST /api/chat/eval` | `POST /api/chat/eval` |
| Body | identical, including the configuration's manifest | identical |
| Engine | the real one, one evaluation per configuration | the real one |
| Delivery | **never** — the server refuses to broadcast a preview's result, and refuses the engine's own deferred submit fire for that stream | delivered on the production path (SSE `input_action`) |

So a preview is *real evaluation with delivery removed*, not a client-side
approximation of the engine — a phrase matcher here would be a second opinion
about the engine's decisions, which is the one thing this page must not be. And a
fire is *the same evaluation with delivery restored*, not a special case.

**Nothing fires implicitly.** The Fire button is disabled until a preview for that
row has succeeded, so the only way to reach a real delivery is a deliberate
second click on a row you already looked at. A preview never delivers: the marker
is on the wire, which means every preview is visible in the command log as
`dropped (preview-suppressed)` — you can read the refusals rather than take them
on faith.

### How preview/fire is gated (security posture)

Three gates, none of them new:

1. **The origin guard.** `/api/chat/eval` is in `guard.GuardedPaths`. The page is
   either served from the server's own origin or dev-proxied so the browser's
   Origin and the request's Host agree; a foreign origin is refused `403` before
   any handler runs. No allowed origin is added and no check is weakened.
2. **The stream marker.** Preview results are never broadcast. This is a
   *subtraction* the server performs on itself, in the same place the capability
   gate already subtracts.
3. **An explicit click.** Fire requires a successful preview of that row first,
   plus a click on that row's own button.

So the sandbox adds no unauthenticated action surface it did not already have:
the only mutating call it can make is the same one any composer in the panel can
make, and it cannot make it without a deliberate user act.

## Configurations

A configuration is what parlay already means by one — a command manifest
(`parlay.commands/v1`, the same document the engine loads from `commands.json` or
`PARLAY_COMMANDS`) plus the platform it applies to. The page hands the chosen
manifest to the server as the **per-request `commands` override**, which the
engine's documented precedence already supports (`request > file > embedded`).

Three built-in configurations ship with the page, and they differ deliberately:
the *same* string resolves to *different* actions under each (one clears the
input, one strips the trigger and keeps the buffer; one auto-submits, one has no
submit command at all). That difference is the property the page exists to make
visible.

To test your own, drop a `sandbox.configs.json` beside the page:

```json
{
  "configs": [
    { "id": "mine", "label": "My configuration", "platform": "parlay",
      "manifest": { "schema": "parlay.commands/v1", "version": "1",
                    "commands": [ /* same shape as commands.json */ ] } }
  ]
}
```

A missing or malformed file is **reported** (the page says which file and why) and
the built-ins are used, rather than the page refusing to start.

## The log and the off switch

`src/LogPanel.tsx` renders `GET /api/chat/action-log`. The filtering is the
**server's** — the page only builds the query, so the view and the API cannot
disagree by construction. The four-value outcome vocabulary
(`delivered`/`queued`/`dropped`/`refused`) is served in the same payload, so the
page offers the closed set rather than guessing. Full write-up:
[`docs/action-log.md`](../../docs/action-log.md).

## Tests

```sh
cd packages/sandbox && bun test
```

- `src/configs.test.ts` — manifest validation, the built-ins genuinely differing.
- `src/api.test.ts` — the preview/fire wire markers, query building, the off
  target a row offers.
- `src/App.test.tsx` — the page: multiple configuration rows, preview stream ids,
  fire stream ids, Fire disabled before a preview, the delivered-action stream,
  the outcome vocabulary, and a row's off switch hitting the real route.

## Layout

| File | What it is |
|---|---|
| `src/configs.ts` | the configurations, and the loading/validation of `sandbox.configs.json` |
| `src/api.ts` | the evaluation up-channel: the preview/fire stream markers and the eval request body |
| `src/log.ts` | the command log's client half: the record shape, the filter vocabulary, the read, and the per-row off target |
| `src/PreviewPanel.tsx` | the per-configuration preview/fire table |
| `src/LogPanel.tsx` | the command log: the filter bar and the table shell |
| `src/LogRows.tsx` | one log row, and the "this is OFF right now" strip above the table |
| `src/App.tsx` | the page shell, the SSE subscription carrying delivered actions |
| `src/testkit.ts` | the fake server and stub SSE stream the component tests drive |

The input wrapper this page's protocol follows is
[`packages/input`](../input) — imported **from source** through a Vite alias
rather than a built `dist`, so there is exactly one implementation of the input
protocol in this repo.
