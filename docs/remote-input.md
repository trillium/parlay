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

## Target enumeration (task-46ys9 — Talon names, not OS names)

Measured live 2026-09-25: submitting the macOS process name fails
verification — the process name for WezTerm is `wezterm-gui` while Talon
reports `WezTerm`, so `POST /submit` with `"app": "wezterm-gui"`
settles `focus_failed` (`focus mismatch: wanted app "wezterm-gui",
active is "WezTerm"`). A phone UI therefore cannot enumerate targets
from OS processes. Target names come from Talon:

`GET /api/chat/remote-input/targets` (200 | 502 Talon unreachable):

```json
{"targets": [{"name": "WezTerm", "focused": true,
 "windowTitle": "macbookpro: coder", "windowCount": 1, "hasWindows": true},
 {"name": "Raycast", "focused": false, "windowCount": 0, "hasWindows": false}]}
```

Per target: `name` (the exact Talon `ui.apps()` name — the same string
focus verification compares, so a picked target round-trips by
construction), `focused` (Talon's active app), the relevant window
title (first window in Talon's per-app ordering) and open-window
count, in Talon's own `ui.apps()` ordering (the spec's
cycling/recently-used behaviour needs the computer's ordering, not
ours). Apps without windows stay listed (the launchable-apps row needs
them) with `"hasWindows": false`. One bounded repl call, read-only —
no focus change, no keystroke — so `?dryRun=1` is an accepted no-op
echoed back (`"dryRun": true) for callers that want the mode visible.

## No-target rule (task-46ys9 — never inject blind)

Measured live: `POST /submit` with no `app` field used to yield
`focus: "not_required"` and, with `dryRun` off, inject into whatever
happens to be focused — on a voice-driven machine that can send
dictation into the wrong application. The no-target case is now
explicit: a live submit with no `app` and no `windowTitle` must set
`"allowUnfocused": true` (or `?allowUnfocused=1`) deliberately, and
the outcome surfaces the mode (`"focus": "unfocused_allowed"` plus
`"allowUnfocused": true`). Without the flag the submit is refused —
400 with a typed error at the wire, `focus_failed` naming the flag for
in-process callers — injecting nothing, preserving the text. Dry runs
type nothing, so they stay exempt: a targetless dry-run submit still
reports `dry_run_passed` with `wouldInsert` (`focus: "not_required"`).

## Bead mode (task-r887x — capture instead of inject)

The same submit path can capture the incoming text as a **bead**
instead of turning it into physical keyboard input. The PWA's deferred
input-mode selector (task-pf1n3) will drive this mode.

`POST /api/chat/remote-input/submit` with `"mode": "bead"`
(or `?mode=bead`; the body wins when both are set):

```json
{"device": "phone-1", "text": "call mom tomorrow", "mode": "bead"}
```

- `mode` is `"inject"` (default) or `"bead"`. Empty/missing means
  `inject`, so every existing caller and the current PWA keep working
  unchanged. Anything else is 400 (`unknown mode`).
- Bead mode **bypasses the whole Talon path**: no focus resolution, no
  `actions.insert`, no target needed. A targetless bead submit is valid
  — the no-target refusal above is inject-only.
- Success settles `bead_created` carrying the new id, the store used,
  and the exact bytes captured (`capturedText`); `injectAttempted` stays
  false and `focus` stays `not_required`. It fans out through the same
  status endpoint and the same `remote_input_result` SSE event, so the
  phone clears its box and shows the id exactly as it does on `injected`.
- Failure settles `bead_failed` with a typed `error` and **no id ever**:
  wrapper missing, non-zero exit (stderr included), unparsable wrapper
  output (`bead created but id unparseable … no id invented` — the bead
  may exist, so no id is guessed), over-long text, or bad store name.
  The text is preserved (`preserveText: true`).
- Captured text is capped at **2000 chars (runes)** — the scratchpad
  precedent. Over-long submits are rejected (400 at the wire,
  `bead_failed` in-process), never truncated.
- `dryRun: true` with `mode: "bead"` creates nothing and reports
  `dry_run_passed` with `wouldInsert` (exact bytes), `beadStore`, and
  `beadWrapper` — exactly what would be captured and what would be
  called.

### Store rule (never a hard-coded set)

Default store is `inbox` (the capture/triage queue per `~/data/README.md`).
The caller may name another store (e.g. `"store": "task"`), and any
registered wrapper name works — the name is validated, not looked up:
it must match `^[a-z][a-z0-9_-]{0,63}$` (a safe single path element, so
it can only resolve to `<dir>/<name>`, never a traversal or flag).
Anything else is 400 / `bead_failed` (`invalid bead store`).

### Explicit wrapper path (correctness, not hygiene)

The server resolves the wrapper to an **explicit path** — never through
an inherited `PATH` alone (a launchd-spawned server has a minimal PATH;
this box has been bitten by that class of bug with `gh`). Order:
`FM_<STORE>_BIN` override (e.g. `FM_INBOX_BIN`, `FM_TASK_BIN`) > known
install locations (`~/.local/bin/<store>`, `~/.pi/agent/bin/<store>`) >
`PATH` lookup. When nothing resolves, the outcome is `bead_failed`
(`bead wrapper not found for store … (tried: …)`). The resolved path is
echoed on every bead outcome as `beadWrapper`, so dry runs audit exactly
what would be called. The text is passed as a **single argv element**
(`wrapper q "<text>"` via `exec`, no shell) — it is arbitrary phone
dictation content and is never interpolated into a command line.

### Bead proof (safe — types nothing, creates one real inbox bead)

Same temp-server recipe as above (temp build dir, temp state dir, temp
port — never the live server), then:

```sh
curl -s -X POST localhost:4499/api/chat/remote-input/submit \
  -H 'Content-Type: application/json' \
  -d '{"device":"manual-1","text":"parlay bead-mode proof ✓","mode":"bead"}'
# → {"id":"ri-1","status":"queued"}
curl -s 'localhost:4499/api/chat/remote-input/status?id=ri-1'
# → {"status":"bead_created","focus":"not_required","mode":"bead",
#      "injectAttempted":false,"beadId":"inbox-xxxx","beadStore":"inbox",
#      "beadWrapper":"/Users/<you>/.local/bin/inbox",
#      "capturedText":"parlay bead-mode proof ✓",...}
BEAD=$(curl -s 'localhost:4499/api/chat/remote-input/status?id=ri-1' | python3 -c 'import json,sys; print(json.load(sys.stdin)["beadId"])')
inbox show "$BEAD"   # the capture landed — close it after to avoid litter
```

Dry-run first if the box is unfamiliar — it resolves the wrapper and
reports `dry_run_passed` with `wouldInsert`/`beadStore`/`beadWrapper`
without creating anything. On a live submit, poll: the fleet wrapper's
external-first routing can take seconds on first call (one observed
`bead_created` settled ~6 s after submit), so the status reads
`injecting` until the wrapper exits.

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

## Targets + no-target proof (safe, runnable now — types nothing)

Same server as above (it serves the live Talon read path; the calls
below never focus and never type):

```sh
curl -s localhost:4499/api/chat/remote-input/targets | head -c 400
# → {"targets":[{"name":"...","focused":...,"windowTitle":"...",
#      "windowCount":N,"hasWindows":true}, ...]}  (Talon order)
curl -s 'localhost:4499/api/chat/remote-input/targets?dryRun=1' | head -c 120
# → {"targets":[...],"dryRun":true}  (read-only; flag is a no-op echo)
curl -s -X POST localhost:4499/api/chat/remote-input/submit \
  -H 'Content-Type: application/json' \
  -d '{"device":"manual-1","text":"blind?"}'
# → 400 {"error":"no target: set app or windowTitle (see GET
#      remote-input/targets for Talon names), or send allowUnfocused:true
#      to inject without focus"}
curl -s -X POST localhost:4499/api/chat/remote-input/submit \
  -H 'Content-Type: application/json' \
  -d '{"device":"manual-1","text":"dry no target","dryRun":true}'
# → {"id":"ri-N","status":"queued"}; status polls to
#   {"status":"dry_run_passed","focus":"not_required","dryRun":true,
#    "wouldInsert":"dry no target",...}  (dry runs stay exempt)
```

Use a `name` from the targets listing verbatim for `"app"` — never the
osascript process name.

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
