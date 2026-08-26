# Out-of-process producers reach the Go SSE hub via `POST /api/chat/events`

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`Hub.broadcast` is in-process-only, so a producer that cannot live in the Go
server had no way to put a frame on the panel. `POST /api/chat/events`
(`packages/go-server/internal/handlers/events_ingress.go`) is that seam: a
`{event, data}` body, an **allowlist** check, then `hub.broadcast`. `data` is
`json.RawMessage` and reaches the wire untouched — the panel must not be able
to tell an ingress frame from an internal one.

The allowlist is **one name per real producer**: `tool_event` alone today, for
the TS tool tailer. Widen it when something in this repo actually produces a
name, and record the producer next to the entry. The tempting larger set —
every `docs/api-contract.md` SSE name with a first-party client subscriber and
no producer inside this server (events.go's "not live" column) — is
deliberately *not* the rule, because it sweeps in the panel-aiming events
(`navigate`, `reload`, `device_cmd`, `input_action`, `draft`), and the guard on
this route allows a missing Origin by design, so any local or LAN process could
reload or navigate every connected panel or overwrite the captain's draft.
Every name the server does produce — `message`, `history`, `agents`,
`agent_register`, `message_received`, `presence_map`, `commands`,
`command_update` — is refused for a second, independent reason: each of those
frames is the server reporting its own persisted state; accepting one from
outside puts a message on the panel that is in no history file and that no
reconnect reproduces. A producer that wants a message broadcast uses `POST
/api/chat/message`, which persists first. Unknown names 400 rather than passing
through: the client's `onSse` shim subscribes to arbitrary names, so a
pass-through would make this a "push any frame to every panel" primitive on an
unauthenticated server.

`system_update` is **not** an event name — it is `ChatMessage.type` carried on
`message` (`packages/client/src/{sse,thread}.ts` branch on it to render a
muted system line and to suppress TTS). It travels through
`/api/chat/message`, which now accepts `type`/`source`/`meta` and stores them
on `store.ChatMessage` (all `omitempty`, so an existing producer's frame is
byte-identical). `type` is the load-bearing one: drop it and every hook firing
renders as an ordinary agent bubble and is spoken aloud. `source` is only the
label on that muted line — drop it and `thread.ts` prints `system` instead.

`/api/chat/events` is consequently in `internal/guard.GuardedPaths`, and since
that classifier is method-independent the **GET stream is now guarded too**.
No real caller notices the refusal — the panel is same-origin and every other
caller sends no Origin — but **guarding a path is not a one-way tightening**:
it also makes the guard reflect an `Access-Control-Allow-Origin` back to every
origin `OriginAllowed` accepts, which is any loopback, `.local` or private-LAN
page. On the SSE stream that would be brand-new read access, since this server
has never sent CORS headers on a read route (divergence 1). `noGuardedCORSReads`
in `guard.go` is the carve-out: for `GET /api/chat/events` the guard sets only
`Vary: Origin` and no CORS headers at all, so the cross-origin refusal is
stricter than the TS side while the grant to allowed origins stays exactly
where it was. The path stays guarded — un-guarding it to drop the ACAO would
drop the 403 too. Anything else here that acquires a mutating method on a
read path needs the same entry.

The two PAI observability tailers are the first callers and stay TS-side (they
read JSONL under `$PAI_DIR` in the Pulse home): `tool-tailer.ts` →
`pushHubEvent("tool_event", …)`, `hook-tailer.ts` → `postHubMessage(…)`, both
via `packages/server/src/hub-ingress.ts`. **Every call there is
fire-and-forget and must never throw into a tailer** — the in-process
broadcast it replaces could not fail, and a tailer that dies on a connection
refused stops tailing for the life of the process. Failures are swallowed and
logged at most once per 30s per route with a suppressed count, and each request
carries a 5s `AbortSignal.timeout` so a wedged hub cannot accumulate pending
requests forever. Posts are **chained per route** rather than fired
concurrently: the in-process calls they replace ran to completion in order, and
a rotated `hook-firings.jsonl` re-reads every line in one synchronous pass, so
unawaited fetches would let the Go server assign `id`/`ts` in arrival order and
land the burst out of order in history and in the thread. `post()` still returns
void immediately and the chain can never reject.

A chain that cannot drain is bounded at `HUB_QUEUE_MAX` (256) posts per route,
past which the newest is shed — but **depth alone never sheds**. A rotated
`hook-firings.jsonl` is re-read in one synchronous pass, and no `.then` can run
until that pass yields, so a perfectly healthy hub legitimately shows a
several-hundred-deep queue for one tick; and `POST /api/chat/message` persists,
so a shed there is a history entry no reconnect brings back, not a stale panel
frame. Shedding therefore also requires the chain to be genuinely stalled — the
head link unanswered for longer than the 5s abort deadline (`stalled()`, off
`unansweredSince`, which any response clears and an idle route resets). A hub
that refuses or errors fast keeps the chain moving and never sheds. Each shed
post goes through the same rate-limited failure path, and a success is not
allowed to clear that limiter while a backlog remains, so sustained shedding
stays one line per 30s per route rather than one per shed post.

The hub URL is `PARLAY_HUB_URL`, falling back to a **fixed**
`http://127.0.0.1:4242` (the Go server's own coded default addr).
`PARLAY_PORT` is deliberately not consulted and must not be re-coupled: this
process reads it as its OWN listen port, so deriving the hub address from it
points the tailers at the TS server, which has neither route — both 404, and
the only symptom is one rate-limited warn line per 30s.
