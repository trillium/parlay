import type { ChatAction } from "./types"
import { saveDraftToDisk } from "./storage"
import { agents, pollWaiters, CORS, broadcastToClients, persistAgents } from "./sse"
import { addMessage, broadcastAlert } from "./messages"
import { unregisterAgent, clearTombstone } from "./prune"
import { handleSubscribersRequest } from "./router-subscribers"
import { loadAgentContext } from "./agent-context"
import { handleMiscRequest } from "./router-misc"

// Best-effort: sync primary nickname to herdr so the terminal tab label matches.
// Fire-and-forget — failure is silently ignored.
function herdrRename(agentId: string, primary: string): void {
  try { Bun.spawn(["herdr", "agent", "rename", agentId, primary], { stdout: "ignore", stderr: "ignore" }) } catch { /* herdr absent */ }
}

// Normalise the nicknames field from a request body:
// - nicknames: [...] → use as-is
// - nickname: "foo"  → [foo]  (backward compat)
// - absent / empty   → fallback
function parseNicknames(body: Record<string, unknown>, fallback?: string[]): string[] | undefined {
  if (Array.isArray(body.nicknames)) {
    const arr = body.nicknames.map(String).map(s => s.trim()).filter(Boolean)
    return arr.length ? arr : fallback
  }
  if (body.nickname != null) {
    const s = String(body.nickname).trim()
    return s ? [s] : fallback
  }
  return fallback
}

// Message creation lives in messages.ts; re-exported so existing importers
// (lavish.ts) keep their "./router-messages" path.
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
          // Try body.agent first; fall back to the server's agent registry,
          // on-disk context.json, and the server's own designated id.
          const agentLookup = loadAgentContext(agents, body.agent ? String(body.agent).trim() : undefined)
          const agentId = agentLookup?.id
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
          let autoRegistered = false
          if (agentId) {
            // Upsert: a poll-auto-registered record (grey, name=id) gets the
            // real name/color on first reply that carries them.
            const existing   = agents.get(agentId)
            // Track whether this is the first time this agent ID has been seen
            // (robots-5l8: silent phantom-tab creation from typo'd agent IDs).
            // Callers that check response.new_channel can detect the mismatch.
            if (!existing) autoRegistered = true
            const name       = String(body.name  ?? existing?.name  ?? agentId).trim() || agentId
            const color      = String(body.color ?? "").trim() || existing?.color || "#3FB950"
            const nicknames  = parseNicknames(body as Record<string, unknown>, existing?.nicknames)
            const urls       = Array.isArray(body.urls)  ? body.urls.map(String).filter((u: string) => u.length > 0)  : existing?.urls
            const path       = Array.isArray(body.path)  ? body.path.map(String).filter((u: string) => u.length > 0)  : existing?.path
            const changed    = !existing
              || existing.name  !== name
              || existing.color !== color
              || JSON.stringify(existing.nicknames ?? []) !== JSON.stringify(nicknames ?? [])
              || JSON.stringify(existing.urls  ?? []) !== JSON.stringify(urls  ?? [])
              || JSON.stringify(existing.path  ?? []) !== JSON.stringify(path  ?? [])
            if (changed) {
              const info = { id: agentId, name, color, ...(nicknames?.length ? { nicknames } : {}), ...(urls?.length ? { urls } : {}), ...(path?.length ? { path } : {}) }
              agents.set(agentId, info)
              broadcastToClients("agent_register", info)
              persistAgents()
              const primary = nicknames?.[0]; const oldPrimary = existing?.nicknames?.[0]
              if (primary && primary !== oldPrimary) herdrRename(agentId, primary)
              if (autoRegistered) console.warn(`[parlay/reply] auto-registered new agent '${agentId}' on first reply — if this is a typo, use a pre-registered id`)
            }
          }
          const msg = addMessage("agent", text || action?.label || "", agentId, {
            ...(action ? { type: "action_request" as const, action } : {}),
            ...(images.length ? { images } : {}),
          })
          broadcastToClients("presence", { status: "idle" })
          // new_channel: true signals the agent id was auto-registered on this
          // reply (robots-5l8 — not pre-registered via /api/chat/register-agent).
          // A wrong id creates an invisible phantom tab; this flag lets callers detect it.
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, id: msg.id, ...(autoRegistered ? { new_channel: true } : {}) })))
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
          const body     = await req.json()
          const id       = String(body.id    ?? "").trim()
          const name     = String(body.name  ?? id).trim() || id
          const color    = String(body.color ?? "").trim() || "#3FB950"
          if (!id) { controller.enqueue(enc.encode(JSON.stringify({ error: "id required" }))); controller.close(); return }
          const existing  = agents.get(id)
          // Unspecified fields fall back to whatever is already persisted — so
          // re-registering without nicknames/urls/path doesn't wipe them. But an
          // EXPLICIT empty `nicknames: []` on this management endpoint is a clear
          // (unlike /send auto-register, where empty must never clobber metadata).
          const explicitClearNicks = Array.isArray(body.nicknames)
            && (body.nicknames as unknown[]).map(String).map(s => s.trim()).filter(Boolean).length === 0
          const nicknames = explicitClearNicks ? undefined : parseNicknames(body as Record<string, unknown>, existing?.nicknames)
          const urls      = Array.isArray(body.urls)  ? body.urls.map(String).filter((u: string) => u.length > 0)  : existing?.urls
          const path      = Array.isArray(body.path)  ? body.path.map(String).filter((u: string) => u.length > 0)  : existing?.path
          // Launch record (task-4dz9): launchedBy sticky once set; startedAt stamped ONCE (first registration wins).
          const launchedBy = String(body.launchedBy ?? "").trim() || existing?.launchedBy
          const startedAt  = existing?.startedAt || (String(body.startedAt ?? "").trim() || undefined)
          const info = { id, name, color, ...(nicknames?.length ? { nicknames } : {}), ...(urls?.length ? { urls } : {}), ...(path?.length ? { path } : {}), ...(launchedBy ? { launchedBy } : {}), ...(startedAt ? { startedAt } : {}) }
          // An explicit register is a deliberate act, so it lifts any tombstone
          // left by a prior prune/unregister — re-arming a real agent whose id
          // was swept must work on the first try, not after the TTL (robots-ycfa).
          clearTombstone(id)
          agents.set(id, info)
          broadcastToClients("agent_register", info)
          persistAgents()
          const primary = nicknames?.[0]; const oldPrimary = existing?.nicknames?.[0]
          if (primary && primary !== oldPrimary) herdrRename(id, primary)
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
      return json({ ok: true, id: res.id, undelivered: res.undelivered })
    })()
  }

  // REST alias for the same removal: DELETE /api/chat/agents/:id. Same fail-loud
  // contract. The id is the trailing path segment, URL-decoded.
  if (req.method === "DELETE" && pathname.startsWith("/api/chat/agents/")) {
    const id = decodeURIComponent(pathname.slice("/api/chat/agents/".length)).trim()
    if (!id) return new Response(JSON.stringify({ error: "id required" }), { status: 400, headers: { "Content-Type": "application/json", ...CORS } })
    const res = unregisterAgent(id)
    if (!res.ok) return new Response(JSON.stringify({ error: res.error }), { status: 404, headers: { "Content-Type": "application/json", ...CORS } })
    return new Response(JSON.stringify({ ok: true, id: res.id, undelivered: res.undelivered }), { headers: { "Content-Type": "application/json", ...CORS } })
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

  const subsResp = handleSubscribersRequest(req, pathname)
  if (subsResp !== null) return subsResp

  // declare-channel + alert live in router-misc.ts (250-line split)
  return handleMiscRequest(req, pathname)
}