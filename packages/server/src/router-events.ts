import { randomUUID } from "crypto"
import { history, historyIndex } from "./storage"
import { sseClients, agents, agentActive, CORS, sseEvent, computePresenceMap } from "./sse"
import { bundleVersion } from "./bundle-version"

export function handleEventsRequest(req: Request, pathname: string): Response | null {
  if (req.method === "GET" && pathname === "/api/chat/history") {
    const rawLimit = new URL(req.url).searchParams.get("limit")
    const parsed   = rawLimit ? parseInt(rawLimit, 10) : NaN
    const limit    = Number.isFinite(parsed) && parsed > 0 ? parsed : 200
    return new Response(JSON.stringify(history.slice(-limit)), {
      headers: { "Content-Type": "application/json", ...CORS },
    })
  }

  if (req.method === "GET" && pathname === "/api/chat/version") {
    return new Response(JSON.stringify({ version: bundleVersion() }), {
      headers: { "Content-Type": "application/json", ...CORS },
    })
  }

  if (req.method === "GET" && pathname === "/api/chat/events") {
    const clientId = randomUUID()
    const url      = new URL(req.url)
    const device   = url.searchParams.get("device") ?? undefined
    const ua       = req.headers.get("user-agent") ?? undefined
    const afterId  = url.searchParams.get("after") ?? undefined
    const afterIdx = afterId ? historyIndex.get(afterId) : undefined
    let initialHistory: typeof history
    if (afterIdx !== undefined) {
      initialHistory = history.slice(afterIdx + 1)
    } else {
      const clientUrl  = url.searchParams.get("url") ?? ""
      const ownerEntry = clientUrl
        ? [...agents.entries()].find(([, a]) => a.urls?.some(u => clientUrl.startsWith(u)))
        : undefined
      const ownerCh    = ownerEntry?.[0]
      const PER_CHANNEL = 50
      const OWNER_LIMIT = 200
      const seen = new Set<string>()
      const channelIds = new Set([...agents.keys(), "system", "__none__"])
      const perChannel: (typeof history[number])[] = []
      for (const ch of channelIds) {
        const lim  = ch === ownerCh ? OWNER_LIMIT : PER_CHANNEL
        const msgs = ch === "__none__"
          ? history.filter(m => !m.channel)
          : history.filter(m => m.channel === ch)
        for (const m of msgs.slice(-lim)) {
          if (!seen.has(m.id)) { seen.add(m.id); perChannel.push(m) }
        }
      }
      initialHistory = perChannel.sort((a, b) =>
        new Date(a.ts).getTime() - new Date(b.ts).getTime()
      )
    }
    const stream = new ReadableStream({
      start(controller) {
        sseClients.set(clientId, { id: clientId, controller, device, ua, connectedAt: new Date().toISOString() })
        const enc = new TextEncoder()
        controller.enqueue(enc.encode(sseEvent("connected",      { clientId })))
        controller.enqueue(enc.encode(sseEvent("history",        initialHistory)))
        controller.enqueue(enc.encode(sseEvent("agents",         Array.from(agents.values()))))
        controller.enqueue(enc.encode(sseEvent("agent_presence", { active: agentActive })))
        controller.enqueue(enc.encode(sseEvent("presence_map",   computePresenceMap())))
        const keepalive = setInterval(() => {
          try { controller.enqueue(enc.encode(": ka\n\n")) }
          catch { clearInterval(keepalive); sseClients.delete(clientId) }
        }, 25_000)
      },
      cancel() { sseClients.delete(clientId) },
    })
    return new Response(stream, {
      headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", "Connection": "keep-alive", ...CORS },
    })
  }

  return null
}
