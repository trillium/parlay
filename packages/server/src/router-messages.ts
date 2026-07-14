import { randomUUID } from "crypto"
import type { ChatMessage } from "./types"
import { pushToHistory, saveDraftToDisk, persistMessage } from "./storage"
import { agents, pollWaiters, sseClients, setAgentPresence, CORS, broadcastToClients, lastPollByChannel, LISTEN_WINDOW_MS, presenceBroadcasts } from "./sse"

// ── Message creation ─────────────────────────────────────────────────────────

// Resolve all matching poll waiters for an alert message (not just one)
function resolveWaiters(msg: ChatMessage) {
  let delivered = 0
  const target = msg.channel
  for (let i = pollWaiters.length - 1; i >= 0; i--) {
    const w = pollWaiters[i]
    if (target ? w.channel === target : !w.channel) {
      pollWaiters.splice(i, 1)
      clearTimeout(w.timer)
      w.resolve(msg)
      delivered++
    }
  }
  if (pollWaiters.length === 0) setAgentPresence(false)
  return delivered
}

// Send an alert to specific agent channels (or all registered agents if none specified).
// Creates one message per channel so each agent receives it on poll.
export function broadcastAlert(text: string, targetAgentIds?: string[]): { channels: number; delivered: number } {
  const channels: (string | undefined)[] = targetAgentIds?.length
    ? targetAgentIds
    : [undefined, ...agents.keys()]   // undefined = global pollers + each named agent

  const now = new Date().toISOString()
  let delivered = 0

  for (const channel of channels) {
    const msg: ChatMessage = {
      id:   crypto.randomUUID(),
      role: "user",
      type: "alert",
      ts:   now,
      text,
      ...(channel ? { channel } : {}),
    }
    pushToHistory(msg)
    persistMessage(msg)
    broadcastToClients("message", msg)
    delivered += resolveWaiters(msg)
  }

  return { channels: channels.length, delivered }
}

export function addMessage(role: "user" | "agent", text: string, channel?: string): ChatMessage {
  const msg: ChatMessage = {
    id:   randomUUID(),
    role,
    ts:   new Date().toISOString(),
    text,
    ...(channel ? { channel } : {}),
  }
  pushToHistory(msg)
  persistMessage(msg)
  broadcastToClients("message", msg)
  if (role === "user") {
    // Route to channel-specific waiter first, then fall back to global (no channel) waiters
    const idx = pollWaiters.findIndex(w => msg.channel ? w.channel === msg.channel : !w.channel)
    if (idx !== -1) {
      const [waiter] = pollWaiters.splice(idx, 1)
      clearTimeout(waiter.timer)
      waiter.resolve(msg)
      if (pollWaiters.length === 0) setAgentPresence(false)
    }
  }
  return msg
}

// ── Message routes ────────────────────────────────────────────────────────────
// POST /api/chat/send, /reply, /register-agent; GET /api/chat/agents, /draft

export function handleMessagesRequest(req: Request, pathname: string): Response | null {

  if (req.method === "POST" && pathname === "/api/chat/send") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body = await req.json()
          const text    = String(body.text    ?? "").trim()
          const toAgent = body.toAgent ? String(body.toAgent).trim() : undefined
          if (!text) { controller.enqueue(enc.encode(JSON.stringify({ error: "empty message" }))); controller.close(); return }
          const msg = addMessage("user", text, toAgent)
          saveDraftToDisk("")
          broadcastToClients("draft",    { text: "" })
          broadcastToClients("presence", { status: "thinking" })
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, id: msg.id })))
        } catch { controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  if (req.method === "POST" && pathname === "/api/chat/reply") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body    = await req.json()
          const text    = String(body.text  ?? "").trim()
          const agentId = body.agent ? String(body.agent).trim() : undefined
          if (!text) { controller.enqueue(enc.encode(JSON.stringify({ error: "empty reply" }))); controller.close(); return }
          if (agentId && !agents.has(agentId)) {
            const name  = String(body.name  ?? agentId).trim() || agentId
            const color = String(body.color ?? "").trim()       || "#3FB950"
            const info  = { id: agentId, name, color }
            agents.set(agentId, info)
            broadcastToClients("agent_register", info)
          }
          const msg = addMessage("agent", text, agentId)
          broadcastToClients("presence", { status: "idle" })
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, id: msg.id })))
        } catch { controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  if (req.method === "POST" && pathname === "/api/chat/register-agent") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body  = await req.json()
          const id    = String(body.id    ?? "").trim()
          const name  = String(body.name  ?? id).trim() || id
          const color = String(body.color ?? "").trim() || "#3FB950"
          if (!id) { controller.enqueue(enc.encode(JSON.stringify({ error: "id required" }))); controller.close(); return }
          const info = { id, name, color }
          agents.set(id, info)
          broadcastToClients("agent_register", info)
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, ...info })))
        } catch { controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  if (req.method === "GET" && pathname === "/api/chat/agents") {
    return new Response(JSON.stringify(Array.from(agents.values())), {
      headers: { "Content-Type": "application/json", ...CORS },
    })
  }

  if (req.method === "GET" && pathname === "/api/chat/subscribers") {
    const polling = pollWaiters.map(w => ({
      channel: w.channel ?? null,
      ...(w.channel && agents.has(w.channel) ? agents.get(w.channel)! : {}),
    }))
    const registered = Array.from(agents.values())
    // Presence: union of registered agents and channels ever seen polling.
    // listening = polled within the window; idle = seen but stale; offline = registered, never seen.
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
    const body = {
      parlay:     { clients: sseClients.size },   // browser tabs with the chat drawer open
      poll:       { count: polling.length, channels: polling },  // agents with live poll connections
      registered: { count: registered.length, agents: registered },
      presence,
      presence_broadcasts: presenceBroadcasts,
    }
    return new Response(JSON.stringify(body, null, 2), {
      headers: { "Content-Type": "application/json", ...CORS },
    })
  }

  if (req.method === "POST" && pathname === "/api/chat/alert") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body = await req.json()
          const text = String(body.text ?? "").trim()
          if (!text) { controller.enqueue(enc.encode(JSON.stringify({ error: "text required" }))); controller.close(); return }
          const agentIds: string[] | undefined = Array.isArray(body.agents) && body.agents.length > 0
            ? (body.agents as unknown[]).map(String)
            : undefined
          const result = broadcastAlert(text, agentIds)
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, ...result })))
        } catch { controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  return null
}
