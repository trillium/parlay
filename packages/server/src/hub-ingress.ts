// ── Go-hub ingress client ────────────────────────────────────────────────────
//
// The two PAI observability tailers (tool-tailer.ts, hook-tailer.ts) read JSONL
// files that live in the TS/Pulse home, so they stay in this process — but the
// panel's `/api/chat/events` connection is moving to the Go server's SSE hub
// (packages/go-server/internal/handlers/events.go). An in-process
// `broadcastToClients` from here would fan out to this process's own SSE client
// map, which after the flip is empty. Both tailers therefore push over HTTP
// instead:
//
//   tool-tailer  ──▶ POST /api/chat/events   (broadcast-only; the ingress
//                                             allowlist in the Go server's
//                                             events_ingress.go)
//   hook-tailer  ──▶ POST /api/chat/message  (persists AND broadcasts, which is
//                                             what addMessage did in-process)
//
// EVERY CALL HERE IS FIRE-AND-FORGET AND MUST NEVER THROW INTO A TAILER. The
// in-process broadcast these replace could not fail; a tailer that dies on a
// connection refused would stop tailing for the life of the process, and during
// the transition the Go server may legitimately not be running at all. So the
// contract is: return immediately, swallow every failure, and log at most one
// line per HUB_LOG_INTERVAL_MS per route with a count of what it stood for.

// Where the Go hub listens. PARLAY_HUB_URL is the explicit override and the one
// to set whenever both servers run side by side. The default is the Go server's
// own coded address (cmd/parlay-server/main.go's defaultAddr) — deliberately NOT
// PARLAY_PORT, which this process reads as ITS OWN listen port (index.ts), so
// coupling the tailer default to it would POST to ourselves whenever a caller
// sets PARLAY_PORT to a non-default value.
const HUB_URL = process.env.PARLAY_HUB_URL ?? "http://127.0.0.1:4242"

// A tailer can emit many events per second, so an unreachable hub must not turn
// into an unreachable-hub log firehose. One line per route per interval,
// carrying how many failures it summarizes.
const HUB_LOG_INTERVAL_MS = 30_000

const failures = new Map<string, { since: number; count: number }>()

function noteFailure(route: string, err: unknown) {
  const now = Date.now()
  const prev = failures.get(route)
  if (prev && now - prev.since < HUB_LOG_INTERVAL_MS) {
    prev.count++
    return
  }
  const suppressed = prev ? ` (${prev.count} more since the last line)` : ""
  failures.set(route, { since: now, count: 0 })
  console.warn(`[hub-ingress] POST ${HUB_URL}${route} failed: ${String(err)}${suppressed}`)
}

function post(route: string, body: unknown): void {
  // No await: the caller is a synchronous tail loop, and the in-process
  // broadcast it replaces returned immediately.
  void fetch(`${HUB_URL}${route}`, {
    method:  "POST",
    headers: { "Content-Type": "application/json" },
    body:    JSON.stringify(body),
  })
    .then(res => {
      // Drain the body so the response socket is released for reuse — Bun
      // otherwise keeps the connection open until GC, churning sockets under a
      // busy tailer. A 4xx here means the payload or the event name is wrong —
      // a bug in the caller, not a transient outage — so it is worth a line,
      // under the same rate limit.
      void res.arrayBuffer().catch(() => {})
      if (!res.ok) noteFailure(route, `HTTP ${res.status}`)
      else failures.delete(route)
    })
    .catch(err => noteFailure(route, err))
}

// Push one SSE event to every client connected to the Go hub. `event` must be
// in that server's ingress allowlist (see events_ingress.go); `data` is
// forwarded to the wire unchanged, so it must be exactly the payload the panel
// already expects for that event name.
export function pushHubEvent(event: string, data: unknown): void {
  post("/api/chat/events", { event, data })
}

// Post a chat message to the Go server, which persists it and broadcasts the
// resulting `message` event — the cross-process equivalent of messages.ts's
// addMessage. Extra fields (type, source, meta, …) ride along in the same body
// so the stored message keeps the shape its producer intended.
export function postHubMessage(
  role: "user" | "agent",
  text: string,
  channel: string,
  extra?: Record<string, unknown>,
): void {
  post("/api/chat/message", { role, text, channel, ...(extra ?? {}) })
}
