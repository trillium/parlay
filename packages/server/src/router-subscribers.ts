import { history } from "./storage"
import { agents, pollWaiters, sseClients, CORS, lastPollByChannel, LISTEN_WINDOW_MS, presenceBroadcasts } from "./sse"
import { suppressedCounts } from "./capability"

// GET /api/chat/subscribers — full presence/memory snapshot used by agents and
// the CLI (`parlay subscribers`). Extracted from router-messages.ts to keep
// that file under the 250-line limit.
export function handleSubscribersRequest(req: Request, pathname: string): Response | null {
  if (req.method !== "GET" || pathname !== "/api/chat/subscribers") return null
  const polling = pollWaiters.map(w => ({
    channel: w.channel ?? null,
    ...(w.channel && agents.has(w.channel) ? agents.get(w.channel)! : {}),
  }))
  const registered = Array.from(agents.values())
  const now = Date.now()
  const channelIds = new Set([...agents.keys(), ...lastPollByChannel.keys()])
  const presence = [...channelIds].map(ch => {
    const last = lastPollByChannel.get(ch)
    const listening = last !== undefined && now - last < LISTEN_WINDOW_MS
    return {
      channel: ch,
      ...(agents.get(ch) ?? {}),
      listening,
      lastSeen: last !== undefined ? new Date(last).toISOString() : null,
      status: listening ? "listening" : last !== undefined ? "idle" : "offline",
    }
  })
  const devices = Array.from(sseClients.values())
    .filter(c => c.device)
    .map(c => ({
      device: c.device, ua: c.ua, connectedAt: c.connectedAt,
      // Declared capability surface, when the connection sent ?caps= — the
      // observability half of the delivery gate (docs/interface-capabilities.md).
      ...(c.caps ? { surface: c.caps.surface, accepts: Object.keys(c.caps.accepts).sort() } : {}),
    }))
  // Every declared connection, device-identified or not — the "declarations"
  // half of the observability contract (docs/interface-capabilities.md pairs
  // it with the suppression counters here). content/interactions are the
  // advisory axes, exposed so producers can consult them before v1 gates them.
  const capabilityDeclarations = Array.from(sseClients.values())
    .filter(c => c.caps)
    .map(c => ({
      surface:      c.caps!.surface,
      accepts:      Object.keys(c.caps!.accepts).sort(),
      content:      c.caps!.content,
      interactions: c.caps!.interactions,
      connectedAt:  c.connectedAt,
      ...(c.device ? { device: c.device } : {}),
    }))
  const mem = process.memoryUsage()
  const historyBytes = history.reduce((n, m) => n + JSON.stringify(m).length, 0)
  const body = {
    parlay:     { clients: sseClients.size },
    poll:       { count: polling.length, channels: polling },
    registered: { count: registered.length, agents: registered },
    presence,
    presence_broadcasts: presenceBroadcasts,
    capability_suppressed: suppressedCounts(),
    capability_declarations: capabilityDeclarations,
    devices,
    memory: {
      rssMB:          +(mem.rss / 1048576).toFixed(1),
      heapUsedMB:     +(mem.heapUsed / 1048576).toFixed(1),
      externalMB:     +(mem.external / 1048576).toFixed(1),
      arrayBuffersMB: +(mem.arrayBuffers / 1048576).toFixed(1),
    },
    history: {
      count:           history.length,
      approxBytes:     historyBytes,
      approxKB:        +(historyBytes / 1024).toFixed(1),
      ssePerConnectKB: +(historyBytes / 1024).toFixed(1),
    },
  }
  return new Response(JSON.stringify(body, null, 2), {
    headers: { "Content-Type": "application/json", ...CORS },
  })
}
