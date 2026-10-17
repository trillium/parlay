# Talk demo: scoped terminal view + event inspector (inbox-syvg)

Two demo surfaces for the Software Crafters accessibility talk (2026-10-20),
served by `packages/go-server` at **`/demo`**. Code:
`packages/go-server/internal/handlers/demo.go`, `demo.html`,
`demoassets/recording.json`. Nothing new is invented underneath: it is the
existing message store, broker and input-seam ledger ([`input-seam.md`](input-seam.md)).

**Repo split.** The slides live in another repo (`voicefirst.dev/slides/`, the
link the phone page already uses). This repo owns the page; the deck only needs
an iframe (below). No change was made to the deck repo.

## What the audience sees

| Surface | What it is | Label |
|---|---|---|
| Read-only terminal | A transcript of ONE channel, `talk-demo`, polled every 0.8 s from `GET /api/chat/demo/view` | `LIVE` |
| Read-only terminal | A synthetic fixture (`recording.json`) replayed on a timer, inlined in the page, **no network request** | `RECORDED` |
| Event inspector | One button sends one event; the page shows **Source / Destination / Payload / Outcome** plus the ledger hops | `[LIVE]` / `[RECORDED]` in the Outcome line |

The badge (text, not only colour), the window title and the scope sentence all
change with the mode. Keys: `L` live, `R` recorded.

It is a rendering of the channel's messages, **not a pty mirror**. A terminal or
pane mirror cannot be scoped to "the demo session" without trusting whatever the
terminal prints, so it was rejected.

## Why nothing else can leak

- The view returns only messages whose channel is exactly `talk-demo`, and only
  ledger hops joined on those message ids (`TestDemoViewIsScopedToTheDemoChannel`).
- It reads no environment variable, path, pane, or token. Message text still goes
  through `redactDemo` (token shapes, `KEY=`/`TOKEN=` assignments, `Bearer ...`,
  `/Users/<name>` paths) as a backstop (`TestDemoViewRedactsSecretsAndHomePaths`).
- The fixture is hand-written and synthetic; a test fails if the redactor would
  change a byte of it (`TestDemoPageInlinesAValidRecordedFixture`).
- All three `/api/chat/demo/*` routes are in `guard.GuardedPaths`. `/demo` is
  static HTML with no secrets (listed in `TestUnguardedRoutes`).
- The page writes with `textContent` only.

## Start (dedicated instance, T-30 min on the presenting laptop)

Do not point the talk at the installed `com.parlay.go-server`: it serves whatever
build it was last deployed with, and a pre-#335 build answers `/demo` with a
544-byte generic shell. Run a dedicated instance built from main.

1. Build: `cd packages/go-server && go build -o /tmp/parlay-demo-server ./cmd/parlay-server`
2. Start it with its own state dir (never `pkill -f`; stop it by pid):
   `mkdir -p /tmp/parlay-demo-state /tmp/parlay-demo-home && HOME=/tmp/parlay-demo-home PARLAY_STATE_HOME=/tmp/parlay-demo-state PAI_DIR=/tmp/parlay-demo-home/pai PARLAY_HUB_URL=http://127.0.0.1:1 /tmp/parlay-demo-server -state-dir /tmp/parlay-demo-state -addr 127.0.0.1:4577`
3. Save the static fallback: `curl -s http://127.0.0.1:4577/demo -o ~/Desktop/demo-recorded.html` (about 13 KB; 544 bytes means the wrong server).
4. Open `http://127.0.0.1:4577/demo` in a **separate window**. Click **Send one demo event**, confirm `LIVE` and a reply, then **Reset demo channel**.

The built-in responder answers by default, so the demo does not depend on a model.
To let a real agent answer, tick **Real agent answers** (per event) or start the
server with `PARLAY_DEMO_RESPONDER=off`, then reply with
`POST /api/chat/reply {"agent":"talk-demo",...}`. The Outcome and hop table update
on their own when the reply arrives. A `parlay listen` agent only works if the
relay is bound to this server (the relay is bound to one server for life).

Keep the default loopback exposure: the demo routes have no auth, so never start
the demo with `-exposure lan/tailnet`.

## Embedding /demo in the deck (iframe)

Verified in real Chrome with a harness page on a different origin (https page, `iframe` at
1280x720, 16:9 scaled 1920x1080 at 0.667, 960x540, 800x450): the page renders, polls, takes
the event, and shows an external agent reply within a poll, all inside the frame.

- **Headers.** `GET /demo` sends `Content-Security-Policy: frame-ancestors 'self' <origins>`
  and nothing else frame-related: no `X-Frame-Options` (it cannot name several origins and
  would fight the allowlist), no `Set-Cookie`, no frame-busting JS, no CORS headers. Before
  this, `/demo` had no framing header at all, so any site could frame it.
- **Allowed origins.** `https://macbook.hippo-tilapia.ts.net:4173` (the tailnet deck),
  `https://voicefirst.dev`, `https://www.voicefirst.dev`, `http://127.0.0.1:4173`,
  `http://localhost:4173`. Add more with `PARLAY_DEMO_FRAME_ANCESTORS` (space or comma
  separated exact `http(s)://host[:port]` origins; `*`, paths and wildcards are dropped).
  The page has no auth, so never widen this to `*`.
- **Same-origin traffic.** The page only calls `/api/chat/demo/*` on its own origin (polling,
  no WebSocket or SSE), so the guard's Origin check sees the server's own origin and passes,
  whatever page frames it.
- **Mixed content.** An https deck may frame `http://127.0.0.1:<port>/demo` (loopback is a
  trustworthy origin) but NOT `http://macbook...ts.net:4242/demo`: `:4242` is a raw TCP
  forward on the tailnet, so that is http and is blocked as mixed content. Present from the
  laptop that runs the server and embed the loopback URL.
- **Sizing.** 1280x720 and any 16:9 scale fit with no scrolling. Below 620px of height the
  layout tightens (960x540 fits); at 800x450 or less it scrolls a little, so keep the frame
  at 960x540 or larger.
- **Focus.** Keys `L`/`R` only reach the page while the frame has focus (click it once);
  the deck's arrow keys stop working until you click the deck again. The browser, not the
  page, owns this.

`bin/parlay-demo-rehearsal` checks the headers, the mixed-content rule and a live exchange in a
few seconds (next section).

## Rehearsal (one command)

`bin/parlay-demo-rehearsal` (about 1 s; `--keep` leaves the exchange on screen). Env:
`PARLAY_DEMO_URL` (default `http://127.0.0.1:4242`), `PARLAY_DEMO_DECK_ORIGIN`,
`PARLAY_DEMO_DECK_URL`. It prints PASS/FAIL/WARN per step with the exact `fix:` and exits 1
if anything blocks: (1) server up, (2) `/demo` is the real page, (3) iframe headers allow the
deck origin and never `*`, (4) mixed content, (5) a scripted fake-agent exchange (event, reply
from `rehearsal-agent`, ledger hop) so the inspector has live content, (6) the deck answers,
(7) the demo channel is reset. Its only writes are `/api/chat/demo/*` and one reply on
channel `talk-demo`. Harness: `bin/parlay-demo-rehearsal.test.sh`.

## Reset

1. Click **Reset demo channel** (or `curl -X POST -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:<port>/api/chat/demo/reset`). Only `talk-demo` messages are removed, and the inspector clears itself.
2. Nuclear reset: stop the demo server by pid, trash `/tmp/parlay-demo-state`, start again.

## If live breaks on stage (three tiers)

1. **Tier 1, LIVE.** After 2 failed polls (about 2 s) the badge reads `LIVE - DISCONNECTED` and the terminal is prefixed `STALE since hh:mm:ss` (UTC, like the transcript). When the server answers again the badge returns to `LIVE`.
2. **Tier 2, press `R`.** Switches to the `RECORDED` badge and replays the fixture with no network. Say aloud that it is a recording. The page never switches mode on its own.
3. **Tier 3, static copy.** If the tab or server is gone, open the saved `~/Desktop/demo-recorded.html` in the browser (Start step 3), then press `R` (or add `?mode=recorded` to its `file://` URL).

**Out of scope on purpose:** a general observability view, a real terminal
mirror, auth for the demo routes (the server has none; see the guard doc in
[`api-contract.md`](api-contract.md)).
