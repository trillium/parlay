# Remote-input injection control plane (task-57ltl)

The missing backend for the Parlay remote/voice input tool: takes text
Parlay has accepted and turns it into real keyboard input on the target
Mac via Talon Voice. Choice of language/shape: **Go in
`packages/go-server`** (the live server path mid Bun→Go cutover), Talon
behind the `TalonAdapter` seam in `internal/remoteinput`, HTTP intake in
`internal/handlers/remoteinput.go`.

Front-end location (checked, not assumed): `packages/input` is the DOM
input wrapper (client side only — `core.ts`/`sse.ts`/`dom.ts`); nothing
in the repo references Talon, so no parallel front end was built.

## Wire shape

`POST /api/chat/remote-input/submit` (202, queued — not done):

```json
{"device": "phone-1", "text": "hello\nworld", "app": "Terminal",
 "windowTitle": "", "trigger": "send it"}
```

`GET /api/chat/remote-input/status?id=ri-3` (200 | 404 unknown id):

```json
{"id": "ri-3", "device": "phone-1", "status": "injected",
 "focus": "verified", "injectAttempted": true}
```

Terminal `status`: `injected` | `focus_failed` | `inject_failed` |
`dry_run_passed` (dry-run: real focus + real verification, nothing typed).
Transient: `queued` | `injecting`. Every terminal outcome also fans out
as the device-scoped SSE event `remote_input_result` carrying the same
Outcome — that event is the clear signal: Parlay clears shared state
**only** on `injected` (never on `dry_run_passed`); on `focus_failed` it preserves the text and
strips `trigger` (the line-ender phrase) so it cannot auto-retry (a dry-run
`focus_failed` keeps `dryRun: true`, reports `wouldInsert`, and does not
strip `trigger`). A dry-run submit sets `"dryRun": true` (or `?dryRun=1`)
and its success outcome carries `dryRun: true` plus `wouldInsert` with the
exact bytes that would have been inserted.

Semantics (from project-1ayr): text injects literally, multiline, in
order; focus completes first — inject only on focus success; a busy
injector queues later submissions FIFO (no interleave, no drop, no
busy-wait); one target computer (the Mac running this server + Talon).

## Talon adapter contract (recorded live 2026-09-25, this Mac)

| Item | Value |
|---|---|
| Transport | `~/.talon/.venv/bin/repl` (stdin Python → stdout+stderr merged result); `TALON_REPL_PATH` overrides (mirrors `talon_mcp/tools/lib/repl.ts`) |
| CLI surface | `bun run ~/.talon/talon_mcp/tools/cli.ts {status,repl,mimic,…}` |
| Insert | `actions.insert(<json-quoted str>)` — one call, literal, no paste split (MVP) |
| Focus | exact case-insensitive match in `ui.apps()` → `App.focus()`; or exact case-insensitive match on `ui.windows()` title → `Window.focus()` |
| Verify | `print(ui.active_app().name)` / `print(<focused window title>)` must equal the requested target exactly (case-insensitive); one read after a 300 ms settle sleep **in our process** — never `actions.sleep` polling on Talon's main thread (brain-15l95). Reads are explicit `print(...)` because a bare expression echoes its Python repr (`'Name'` with quotes) and never matches; the adapter additionally strips one repr-quote layer and merges stdout+stderr (current repl.py writes every result to stderr) |
| Verified live | `callable(actions.insert/key)` → True; `ui.apps/active_app/windows` live; `App.focus`/`Window.focus` exist; `1+1`→`2`, `print` round-trip ok; Talon running |
| `talon_mcp` CLI caveat | `talon-cli repl <code>` returns `{"success":true,"output":""}` for EVERY call (even `print("x")`): `tools/lib/repl.ts` captures stdout only, but current repl.py writes all results to stderr. Re-verify any adapter claim via the raw repl (`~/.talon/.venv/bin/repl`); the parlay adapter merges both streams so it is unaffected. Fix belongs to the talon_mcp repo, not parlay |

## Manual proof (background — legs split into Proof status below)

Proven in this change: everything except the final keystroke-delivery
leg (tests: literal multiline, FIFO order, focus-failure typing,
success signal; live: the whole adapter contract above, read-only).
The worker deliberately never ran `actions.insert` here — it types into
whatever owns focus, and this box is the captain's live machine.

## Proof status

- PROVEN (2026-09-25, verify-fix): focus + verification success leg via
  `dryRun` (real focus request, real settle, real verify, nothing typed);
  focus-failure leg (`focus_failed`, nothing typed, text preserved).
- NOT PROVEN: the final `actions.insert` keystroke delivery into a live
  app — dry-run stops short of it by design, and typing into the live
  machine is out of bounds for an agent. Prove it with a visible scratch
  target + eyes (below). Until then the `injected` status means the
  pipeline believes focus verified and insert returned without error,
  not that a human saw characters land.

## Dry-run proof (safe, runnable now — types nothing)

The module is `parlay/go-server` rooted at `packages/go-server`, so
build from there into a temp path (never `go run` from the repo root —
there is no `go.mod` there — and never leave a built binary in the repo).
Target the app that is ALREADY frontmost so the focus request is a no-op.

```sh
cd packages/go-server
BUILD_DIR=$(mktemp -d)   # capture the dir: mktemp honors $TMPDIR, not /tmp
go build -o "$BUILD_DIR/parlay-server" ./cmd/parlay-server  # repo stays clean
STATE_DIR=$(mktemp -d)
"$BUILD_DIR/parlay-server" -addr 127.0.0.1:4499 -state-dir "$STATE_DIR" &  # note pid
curl -s -X POST localhost:4499/api/chat/remote-input/submit \
  -H 'Content-Type: application/json' \
  -d '{"device":"manual-1","text":"dry ✓ proof\nsecond line","app":"<FrontmostAppName>","trigger":"send it","dryRun":true}'
# → {"id":"ri-1","status":"queued"}
curl -s 'localhost:4499/api/chat/remote-input/status?id=ri-1'
# → {"status":"dry_run_passed","focus":"verified","dryRun":true,
#      "injectAttempted":false,"wouldInsert":"dry ✓ proof\nsecond line",...}
```

`wouldInsert` must equal the submitted text byte-for-byte (newline,
non-ASCII included). `?dryRun=1` on the submit URL forces the same mode
without touching the body. Then POST with `"app": "NoSuchAppXYZ"`
and confirm `focus_failed` with `injectAttempted: false`, text preserved
(nothing typed), and afterwards kill the server +
`rm -rf "$STATE_DIR" "$BUILD_DIR"`.

## Live-keystroke proof (needs a visible scratch target + eyes)

Same server as above, then:

```sh
open -a TextEdit            # new scratch document, click into it
FRONT=$(osascript -e 'tell application "System Events" to get name of first process whose frontmost is true')
curl -s -X POST localhost:4499/api/chat/remote-input/submit \
  -H 'Content-Type: application/json' \
  -d "{\"device\":\"manual-1\",\"text\":\"parlay remote-input live ✓\nsecond line\",\"app\":\"$FRONT\",\"trigger\":\"send it\"}"
# → {"id":"ri-2","status":"queued"}
curl -s 'localhost:4499/api/chat/remote-input/status?id=ri-2'
# → {"status":"injected","focus":"verified","injectAttempted":true,...}
```

Expected observation: the exact two lines (incl. `✓` and the newline)
appear at the TextEdit cursor, byte-for-byte; the status poll ends at
`injected`. NOTE: `$FRONT` is the *process* name (e.g. `wezterm-gui`)
while Talon matches the *app* name (e.g. `WezTerm`) — use the Talon
`ui.apps()` name for `"app"`, not the osascript name.
