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

// Where the Go hub listens. PARLAY_HUB_URL is the ONLY override, and the fixed
// fallback is the Go server's own coded default addr (see
// packages/go-server/cmd/parlay-server/main.go).
//
// PARLAY_PORT is deliberately NOT consulted, and must not be re-coupled here:
// this process reads PARLAY_PORT as ITS OWN listen port (index.ts), so deriving
// the hub address from it points the tailers at the TS server itself, which has
// no POST /api/chat/events and no /api/chat/message at all — both routes 404
// and the only symptom is one rate-limited warn line per 30s. Set
// PARLAY_HUB_URL explicitly whenever the Go hub is not on the default port.
const HUB_URL = process.env.PARLAY_HUB_URL ?? "http://127.0.0.1:4242"

// A tailer can emit many events per second, so an unreachable hub must not turn
// into an unreachable-hub log firehose. One line per route per interval,
// carrying how many failures it summarizes.
const HUB_LOG_INTERVAL_MS = 30_000

// Nothing bound to HUB_URL is obliged to answer — a wedged process or a
// half-open connection would otherwise leave one pending request per tailer
// tick, forever. The AbortError lands in the same rate-limited catch below.
const HUB_TIMEOUT_MS = 5_000

const failures = new Map<string, { since: number; count: number }>()

// One in-flight request per route, chained. The in-process calls these replace
// (broadcastToClients, addMessage) ran to completion before their caller's next
// line, so a burst — hook-firings.jsonl rotating, which resets byteOffset to 0
// and re-reads every line in one synchronous pass — used to be delivered and
// persisted strictly in file order. Firing N unawaited fetches instead lets the
// server assign id/ts in arrival order, so the same burst can land out of order
// in history and in the panel thread. Chaining restores the ordering without
// changing the fire-and-forget contract: post() still returns void immediately,
// and the chain can never reject because every link ends in a catch.
const queues = new Map<string, Promise<void>>()

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
  // Serialized in the enqueue order, but never awaited by the caller: the
  // caller is a synchronous tail loop, and the in-process broadcast this
  // replaces returned immediately.
  const payload = JSON.stringify(body)
  const next = (queues.get(route) ?? Promise.resolve())
    .then(() =>
      fetch(`${HUB_URL}${route}`, {
        method:  "POST",
        headers: { "Content-Type": "application/json" },
        body:    payload,
        signal:  AbortSignal.timeout(HUB_TIMEOUT_MS),
      }),
    )
    .then(res => {
      // A 4xx here means the payload or the event name is wrong — a bug in the
      // caller, not a transient outage — so it is worth a line, under the same
      // rate limit.
      if (!res.ok) noteFailure(route, `HTTP ${res.status}`)
      else failures.delete(route)
    })
    // The one catch the whole link needs: it absorbs a synchronous throw from
    // the fetch call, the transport rejection, and anything the response
    // handler throws, so the chained promise always settles fulfilled and the
    // next post() on this route can never inherit a rejection.
    .catch(err => noteFailure(route, err))
  queues.set(route, next)
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
