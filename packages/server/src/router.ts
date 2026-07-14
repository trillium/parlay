import { randomUUID } from "crypto"
import { history, historyIndex, currentDraft, saveDraftToDisk } from "./storage"
import { sseClients, agents, agentActive, pollWaiters, setAgentPresence, CORS, sseEvent, broadcastToClients, lastPollByChannel, broadcastPresenceMap } from "./sse"
import { handleMessagesRequest } from "./router-messages"
import { handleSettings } from "./settings"
import type { PollWaiter } from "./types"

export function handleChatRequest(req: Request, pathname: string): Response | null {
  if (!pathname.startsWith("/api/chat")) return null

  if (req.method === "OPTIONS") return new Response(null, { status: 204, headers: CORS })

  // Parlay settings: GET/PUT /api/chat/parlay/settings
  const settingsResp = handleSettings(req, pathname)
  if (settingsResp !== null) return settingsResp

  // Delegate message-centric routes (send, reply, register-agent, agents)
  const msgResp = handleMessagesRequest(req, pathname)
  if (msgResp !== null) return msgResp

  if (req.method === "GET" && pathname === "/api/chat/history") {
    // Bounded by default — a bare call returns at most 200 messages; pass
    // ?limit=N for more (or fewer). Invalid/absent limit falls back to 200.
    const rawLimit = new URL(req.url).searchParams.get("limit")
    const parsed   = rawLimit ? parseInt(rawLimit, 10) : NaN
    const limit    = Number.isFinite(parsed) && parsed > 0 ? parsed : 200
    return new Response(JSON.stringify(history.slice(-limit)), {
      headers: { "Content-Type": "application/json", ...CORS },
    })
  }

  if (req.method === "GET" && pathname === "/api/chat/events") {
    const clientId = randomUUID()
    const stream = new ReadableStream({
      start(controller) {
        sseClients.set(clientId, { id: clientId, controller })
        const enc = new TextEncoder()
        controller.enqueue(enc.encode(sseEvent("connected",      { clientId })))
        controller.enqueue(enc.encode(sseEvent("history",        history)))
        controller.enqueue(enc.encode(sseEvent("agents",         Array.from(agents.values()))))
        controller.enqueue(enc.encode(sseEvent("agent_presence", { active: agentActive })))
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

  if (req.method === "GET" && pathname === "/api/chat/draft") {
    return new Response(JSON.stringify({ text: currentDraft }), {
      headers: { "Content-Type": "application/json", ...CORS },
    })
  }

  if (req.method === "PUT" && pathname === "/api/chat/draft") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body = await req.json()
          const text = String(body.text ?? "")
          saveDraftToDisk(text)
          broadcastToClients("draft", { text })
          controller.enqueue(enc.encode(JSON.stringify({ ok: true })))
        } catch { controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  if (req.method === "POST" && pathname === "/api/chat/reload") {
    broadcastToClients("reload", {})
    return new Response(JSON.stringify({ ok: true, clients: sseClients.size }), {
      headers: { "Content-Type": "application/json", ...CORS },
    })
  }

  if (req.method === "POST" && pathname === "/api/chat/navigate") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body = await req.json()
          const url = String(body.url ?? "").trim()
          if (!url) { controller.enqueue(enc.encode(JSON.stringify({ error: "url required" }))); controller.close(); return }
          const openDrawer = body.open_drawer === true
          broadcastToClients("navigate", { url, openDrawer })
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, clients: sseClients.size, url, openDrawer })))
        } catch { controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  if (req.method === "GET" && pathname === "/api/chat/poll") {
    const params  = new URL(req.url).searchParams
    const afterId = params.get("after") ?? ""
    const channel = params.get("channel") ?? undefined  // undefined = global (no filter)
    if (channel) {
      lastPollByChannel.set(channel, Date.now())
      broadcastPresenceMap()
    }
    const afterIdx = afterId ? (historyIndex.get(afterId) ?? -1) : -1
    const pending  = history.slice(afterIdx + 1).filter(m =>
      m.role === "user" && (channel ? m.channel === channel : !m.channel)
    )
    if (pending.length > 0) {
      return new Response(JSON.stringify(pending[0]), {
        headers: { "Content-Type": "application/json", ...CORS },
      })
    }
    return new Response(new ReadableStream({
      start(controller) {
        const enc = new TextEncoder()
        const timer = setTimeout(() => {
          const idx = pollWaiters.indexOf(waiter)
          if (idx !== -1) pollWaiters.splice(idx, 1)
          if (pollWaiters.length === 0) setAgentPresence(false)
          broadcastPresenceMap()
          controller.enqueue(enc.encode(JSON.stringify({ timeout: true })))
          controller.close()
        }, 30_000)
        const waiter: PollWaiter = {
          resolve(msg) { controller.enqueue(enc.encode(JSON.stringify(msg))); controller.close() },
          timer,
          channel,
        }
        pollWaiters.push(waiter)
        setAgentPresence(true)
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  return null
}
