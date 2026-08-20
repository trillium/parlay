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

// Serialization means an unresponsive hub converts every tailer tick into a
// queued link that cannot start until the one ahead of it times out, so the
// chain grows at the tailer's rate and each link holds its payload string.
// HUB_QUEUE_MAX bounds that per route — but depth alone does not mean the hub
// is in trouble: a rotated hook-firings.jsonl re-read is one synchronous pass
// that enqueues every line before a single response can be observed, so a
// healthy hub legitimately shows a deep queue for one tick. POST
// /api/chat/message PERSISTS, so shedding one there is a history entry that no
// reconnect brings back, not a stale panel frame. Shedding is therefore gated
// on the chain being genuinely stalled as well — see stalled() below.
const HUB_QUEUE_MAX = 256

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
//
// depth is what is enqueued-but-not-settled on that route. unansweredSince is
// when the current unbroken run of links that never reached a response began —
// null whenever the hub last answered, or whenever the route is idle — so it
// measures how long the head has been waiting on a hub that is not talking,
// which is what separates a wedged hub from a deep-but-draining one.
type RouteQueue = { tail: Promise<void>; depth: number; unansweredSince: number | null }
const queues = new Map<string, RouteQueue>()

// A hub that refuses or errors fast keeps the chain moving and never trips
// this; one that accepts the connection and then says nothing does, once the
// head has been in flight past the abort deadline it will be killed at.
function stalled(queue: RouteQueue): boolean {
  return queue.unansweredSince !== null && Date.now() - queue.unansweredSince >= HUB_TIMEOUT_MS
}

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
  const queue = queues.get(route) ?? { tail: Promise.resolve(), depth: 0, unansweredSince: null }
  queues.set(route, queue)

  if (queue.depth >= HUB_QUEUE_MAX && stalled(queue)) {
    // Reported through the same rate limiter, so a hub that is down for an hour
    // still costs one line per 30s rather than one per shed post.
    noteFailure(route, `backlog of ${queue.depth} unsent posts against a hub that has not answered in ${HUB_TIMEOUT_MS}ms; shed this one`)
    return
  }

  const payload = JSON.stringify(body)
  queue.depth++
  queue.tail = queue.tail
    .then(() => {
      queue.unansweredSince ??= Date.now()
      return fetch(`${HUB_URL}${route}`, {
        method:  "POST",
        headers: { "Content-Type": "application/json" },
        body:    payload,
        signal:  AbortSignal.timeout(HUB_TIMEOUT_MS),
      })
    })
    .then(res => {
      // Any response at all — including a 4xx — is the hub talking, so the
      // chain is moving and nothing may be shed off the back of it.
      queue.unansweredSince = null
      // A 4xx here means the payload or the event name is wrong — a bug in the
      // caller, not a transient outage — so it is worth a line, under the same
      // rate limit.
      if (!res.ok) noteFailure(route, `HTTP ${res.status}`)
      // Clearing the limiter mid-backlog would re-arm the very first line for
      // every post behind it, so sustained shedding would print one warn per
      // shed post. Only a route that has fully drained is quiet again.
      else if (queue.depth <= 1) failures.delete(route)
      // Nothing reads these bodies, and an unread one holds its connection out
      // of the keep-alive pool until GC gets to it. Cancel rather than await:
      // the next queued post must not wait on a body no one wants, and a
      // cancel rejection is not a delivery failure, so it never reaches
      // noteFailure.
      res.body?.cancel().catch(() => {})
    })
    // The one catch the whole link needs: it absorbs a synchronous throw from
    // the fetch call, the transport rejection, and anything the response
    // handler throws, so the chained promise always settles fulfilled and the
    // next post() on this route can never inherit a rejection.
    .catch(err => noteFailure(route, err))
    .finally(() => {
      queue.depth--
      // Nothing is in flight, so there is no stall to carry forward: a later
      // burst must get its own chance at the hub rather than inherit a verdict
      // from whenever it was last seen.
      if (queue.depth === 0) queue.unansweredSince = null
    })
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
