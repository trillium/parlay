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

## Start

1. Run the server: `cd packages/go-server && go run ./cmd/parlay-server`
   (or the installed `com.parlay.go-server`). For rehearsal use an isolated one:
   `-state-dir <tmp> -addr 127.0.0.1:4577` (see `examples/bootstrap-sandbox.sh`).
2. Open `http://127.0.0.1:<port>/demo` (LIVE) or `.../demo?mode=recorded`.
3. Click **Send one demo event**. The built-in responder answers, so the demo does
   not depend on a model. To let a real agent answer instead, start the server
   with `PARLAY_DEMO_RESPONDER=off` and run `parlay listen --agent talk-demo`;
   the agent replies with `POST /api/chat/reply {"agent":"talk-demo",...}`.
4. Embed in the deck (deck repo): `<iframe src="http://127.0.0.1:<port>/demo" title="Parlay live demo" width="100%" height="600"></iframe>`

## Reset

1. Click **Reset demo channel** (or `curl -X POST -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:<port>/api/chat/demo/reset`). Only `talk-demo` messages are removed.
2. Reload the page. Ledger hops for deleted messages drop out of the view with them.

## If live breaks on stage

Press `R`. The page switches to the `RECORDED` badge and replays the fixture
without touching the server. If the server itself is down the page cannot load:
start it again, or serve a rehearsal copy first. The page never switches mode on
its own, so the badge cannot lie.

**Out of scope on purpose:** a general observability view, a real terminal
mirror, auth for the demo routes (the server has none; see the guard doc in
[`api-contract.md`](api-contract.md)).
