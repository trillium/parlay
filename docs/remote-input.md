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

Terminal `status`: `injected` | `focus_failed` | `inject_failed`.
Transient: `queued` | `injecting`. Every terminal outcome also fans out
as the device-scoped SSE event `remote_input_result` carrying the same
Outcome — that event is the clear signal: Parlay clears shared state
**only** on `injected`; on `focus_failed` it preserves the text and
strips `trigger` (the line-ender phrase) so it cannot auto-retry.

Semantics (from project-1ayr): text injects literally, multiline, in
order; focus completes first — inject only on focus success; a busy
injector queues later submissions FIFO (no interleave, no drop, no
busy-wait); one target computer (the Mac running this server + Talon).

## Talon adapter contract (recorded live 2026-09-25, this Mac)

| Item | Value |
|---|---|
| Transport | `~/.talon/.venv/bin/repl` (stdin Python → stdout result); `TALON_REPL_PATH` overrides (mirrors `talon_mcp/tools/lib/repl.ts`) |
| CLI surface | `bun run ~/.talon/talon_mcp/tools/cli.ts {status,repl,mimic,…}` |
| Insert | `actions.insert(<json-quoted str>)` — one call, literal, no paste split (MVP) |
| Focus | match in `ui.apps()` → `App.focus()`; or match `ui.windows()` title → `Window.focus()` |
| Verify | `ui.active_app().name` / focused window title; one read after a 300 ms settle sleep **in our process** — never `actions.sleep` polling on Talon's main thread (brain-15l95) |
| Verified live | `callable(actions.insert/key)` → True; `ui.apps/active_app/windows` live; `App.focus`/`Window.focus` exist; `1+1`→`2`, `print` round-trip ok; Talon running |

## Manual proof (runnable — firstmate, not yet run end-to-end)

Proven in this change: everything except the final keystroke-delivery
leg (tests: literal multiline, FIFO order, focus-failure typing,
success signal; live: the whole adapter contract above, read-only).
The worker deliberately never ran `actions.insert` here — it types into
whatever owns focus, and this box is the captain's live machine.

To prove the last leg (needs a visible scratch target + eyes):

```sh
cd /path/to/parlay
PARLAY_STATE_HOME=$(mktemp -d) go run ./packages/go-server/cmd/parlay-server \
  -addr 127.0.0.1:4499 -state-dir "$PARLAY_STATE_HOME" &  # note pid
open -a TextEdit            # new scratch document, click into it
FRONT=$(osascript -e 'tell application "System Events" to get name of first process whose frontmost is true')
curl -s -X POST localhost:4499/api/chat/remote-input/submit \
  -H 'Content-Type: application/json' \
  -d "{\"device\":\"manual-1\",\"text\":\"parlay remote-input live ✓\nsecond line\",\"app\":\"$FRONT\",\"trigger\":\"send it\"}"
# → {"id":"ri-1","status":"queued"}
curl -s 'localhost:4499/api/chat/remote-input/status?id=ri-1'
# → {"status":"injected","focus":"verified","injectAttempted":true,...}
```

Expected observation: the exact two lines (incl. `✓` and the newline)
appear at the TextEdit cursor, byte-for-byte; the status poll ends at
`injected`. Then also POST with `"app": "NoSuchAppXYZ"` and confirm
`focus_failed` with `injectAttempted: false`, text preserved (nothing
typed), and afterwards kill the server + `rm -rf "$PARLAY_STATE_HOME"`.
