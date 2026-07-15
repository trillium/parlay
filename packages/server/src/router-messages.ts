import type { ChatAction } from "./types"
import { saveDraftToDisk } from "./storage"
import { agents, pollWaiters, sseClients, CORS, broadcastToClients, lastPollByChannel, LISTEN_WINDOW_MS, persistAgents, presenceBroadcasts } from "./sse"
import { addMessage, broadcastAlert } from "./messages"
import { unregisterAgent } from "./prune"

// Message creation lives in messages.ts; re-exported so existing importers
// (lavish.ts, hook-tailer.ts) keep their "./router-messages" path.
export { addMessage, broadcastAlert }

// ── Message routes ────────────────────────────────────────────────────────────
// POST /api/chat/send, /reply, /register-agent; GET /api/chat/agents, /draft

export function handleMessagesRequest(req: Request, pathname: string): Response | Promise<Response> | null {

  if (req.method === "POST" && pathname === "/api/chat/send") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body = await req.json()
          const text    = String(body.text    ?? "").trim()
          const toAgent = body.toAgent ? String(body.toAgent).trim() : undefined
          const images  = Array.isArray(body.images) ? body.images.slice(0, 8).map(String).filter((u: string) => u.length <= 500) : []
          if (!text && !images.length) { controller.enqueue(enc.encode(JSON.stringify({ error: "empty message" }))); controller.close(); return }
          // Agent contract (#17 addendum): image URLs also ride in the text so
          // every poll/monitor consumer sees them; agents map the URL path to
          // ~/exchange/parlay-uploads/<name> and Read the file (see uploads.ts)
          const missing = images.filter((u: string) => !text.includes(u))
          const outText = missing.length ? (text ? `${text}\n${missing.join("\n")}` : missing.join("\n")) : text
          // Sender attribution (#19): relays/intake pages set from so their
          // messages never masquerade as the captain; absent = the captain
          const from = body.from ? String(body.from).trim().slice(0, 40) : undefined
          const msg = addMessage("user", outText, toAgent, {
            ...(images.length ? { images } : {}),
            ...(from ? { from } : {}),
          })
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
          // Optional action payload → message becomes an inline suggestion card
          let action: ChatAction | undefined
          if (body.action && typeof body.action === "object") {
            const kind = String(body.action.kind ?? "")
            if (kind === "navigate" || kind === "switch_tab") {
              action = {
                kind,
                ...(body.action.url     ? { url:     String(body.action.url) }     : {}),
                ...(body.action.channel ? { channel: String(body.action.channel) } : {}),
                label: String(body.action.label ?? "").trim() || (kind === "navigate" ? "Open page" : "Switch tab"),
              }
            } else {
              controller.enqueue(enc.encode(JSON.stringify({ error: `unknown action kind: ${kind}` }))); controller.close(); return
            }
          }
          const images = Array.isArray(body.images) ? body.images.slice(0, 8).map(String).filter((u: string) => u.length <= 500) : []
          if (!text && !action && !images.length) { controller.enqueue(enc.encode(JSON.stringify({ error: "empty reply" }))); controller.close(); return }
          if (agentId) {
            // Upsert: a poll-auto-registered record (grey, name=id) gets the
            // real name/color on first reply that carries them.
            const existing = agents.get(agentId)
            const name  = String(body.name  ?? existing?.name ?? agentId).trim() || agentId
            const color = String(body.color ?? "").trim() || existing?.color || "#3FB950"
            if (!existing || existing.name !== name || existing.color !== color) {
              const info = { id: agentId, name, color }
              agents.set(agentId, info)
              broadcastToClients("agent_register", info)
              persistAgents()
            }
          }
          const msg = addMessage("agent", text || action?.label || "", agentId, {
            ...(action ? { type: "action_request" as const, action } : {}),
            ...(images.length ? { images } : {}),
          })
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
          persistAgents()
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, ...info })))
        } catch { controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  // Deregister a channel from the registry. The counterpart to register-agent:
  // the deregistration-on-exit contract (see prune.ts) says whatever spawned a
  // channel should call this when it finishes; the periodic prune sweep is the
  // belt-and-suspenders for when it doesn't. Fails LOUD (400/404) on a bad or
  // unknown id — never a false ok (robots-5l8).
  if (req.method === "POST" && pathname === "/api/chat/unregister") {
    // Non-streaming (unlike the sibling POST routes) so the HTTP status can
    // actually reflect the outcome — a fail-loud contract needs 400/404, not a
    // 200 body that says "error". Awaiting req.json() here is fine; the caller
    // is a single small POST, not a long-poll.
    return (async () => {
      const json = (b: unknown, status = 200) =>
        new Response(JSON.stringify(b), { status, headers: { "Content-Type": "application/json", ...CORS } })
      let id: string
      try {
        const body = await req.json()
        id = String(body.id ?? "").trim()
      } catch { return json({ error: "bad request" }, 400) }
      if (!id) return json({ error: "id required" }, 400)
      const res = unregisterAgent(id)
      if (!res.ok) return json({ error: res.error }, 404)
      return json({ ok: true, id: res.id })
    })()
  }

  // REST alias for the same removal: DELETE /api/chat/agents/:id. Same fail-loud
  // contract. The id is the trailing path segment, URL-decoded.
  if (req.method === "DELETE" && pathname.startsWith("/api/chat/agents/")) {
    const id = decodeURIComponent(pathname.slice("/api/chat/agents/".length)).trim()
    if (!id) return new Response(JSON.stringify({ error: "id required" }), { status: 400, headers: { "Content-Type": "application/json", ...CORS } })
    const res = unregisterAgent(id)
    if (!res.ok) return new Response(JSON.stringify({ error: res.error }), { status: 404, headers: { "Content-Type": "application/json", ...CORS } })
    return new Response(JSON.stringify({ ok: true, id: res.id }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  // Hooks (and other system components) announce themselves into the chat.
  // Renders as a thin muted system line in every tab; never resolves poll waiters.
  if (req.method === "POST" && pathname === "/api/chat/system") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body   = await req.json()
          const text   = String(body.text   ?? "").trim()
          const source = String(body.source ?? "").trim() || undefined
          if (!text) { controller.enqueue(enc.encode(JSON.stringify({ error: "text required" }))); controller.close(); return }
          const msg = addMessage("agent", text.slice(0, 500), "system", { type: "system_update", ...(source ? { source } : {}) })
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, id: msg.id })))
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
    // Connected SSE devices — lets an agent see which devices exist and address
    // one via POST /navigate|/reload { device }.
    const devices = Array.from(sseClients.values())
      .filter(c => c.device)
      .map(c => ({ device: c.device, ua: c.ua, connectedAt: c.connectedAt }))
    const body = {
      parlay:     { clients: sseClients.size },   // browser tabs with the chat drawer open
      poll:       { count: polling.length, channels: polling },  // agents with live poll connections
      registered: { count: registered.length, agents: registered },
      presence,
      presence_broadcasts: presenceBroadcasts,
      devices,
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
