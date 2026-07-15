import { randomUUID } from "crypto"
import { history, historyIndex, rebuildHistoryIndex, currentDraft, saveDraftToDisk } from "./storage"
import { sseClients, agents, agentActive, pollWaiters, setAgentPresence, CORS, sseEvent, broadcastToClients, broadcastToDevice, lastPollByChannel, computePresenceMap, broadcastPresenceMap, persistAgents } from "./sse"
import { handleMessagesRequest } from "./router-messages"
import { handleParlaySettings } from "./parlay-settings"
import { handleTTSRequest } from "./tts"
import { handleUploadRequest } from "./uploads"
import { handlePluginsRequest } from "./plugins"
import { handleEvalRequest } from "./eval-relay"
import { handleTTSValidateRequest } from "./tts-validate"
import { bundleVersion } from "./bundle-version"
import type { PollWaiter } from "./types"

export function handleChatRequest(req: Request, pathname: string): Response | Promise<Response | null> | null {
  if (!pathname.startsWith("/api/chat")) return null

  if (req.method === "OPTIONS") return new Response(null, { status: 204, headers: CORS })

  // Server-side eval (feat/server-side-eval): async routes that await the Go
  // engine; the outer Bun fetch handler (async) resolves the returned Promise.
  if (pathname === "/api/chat/eval" || pathname === "/api/chat/eval-push") return handleEvalRequest(req, pathname)

  // Parlay settings: GET/PUT /api/chat/parlay/settings
  const parlayResp = handleParlaySettings(req, pathname)
  if (parlayResp !== null) return parlayResp

  // Server TTS: POST /api/chat/tts → audio/wav via the speak daemon
  const ttsResp = handleTTSRequest(req, pathname)
  if (ttsResp !== null) return ttsResp

  // Image uploads: POST /api/chat/upload, GET /api/chat/uploads/<name>
  const uploadResp = handleUploadRequest(req, pathname)
  if (uploadResp !== null) return uploadResp

  // Plugins: GET /api/chat/plugins (manifest), /api/chat/plugin/<id>/*
  const pluginResp = handlePluginsRequest(req, pathname)
  if (pluginResp !== null) return pluginResp

  // Delegate message-centric routes (send, reply, register-agent, agents)
  const msgResp = handleMessagesRequest(req, pathname)
  if (msgResp !== null) return msgResp

  // TTS split quality validation (local Ollama)
  const ttsValidResp = handleTTSValidateRequest(req, pathname)
  if (ttsValidResp !== null) return ttsValidResp

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
    const device = new URL(req.url).searchParams.get("device") ?? undefined
    const ua     = req.headers.get("user-agent") ?? undefined
    const stream = new ReadableStream({
      start(controller) {
        sseClients.set(clientId, { id: clientId, controller, device, ua, connectedAt: new Date().toISOString() })
        const enc = new TextEncoder()
        controller.enqueue(enc.encode(sseEvent("connected",      { clientId })))
        controller.enqueue(enc.encode(sseEvent("history",        history)))
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

  // Served-bundle version — clients compare against their compiled-in version
  // on every SSE (re)connect and self-reload when stale (PWA pages live for
  // days; this is the root fix for phones running old bundles).
  if (req.method === "GET" && pathname === "/api/chat/version") {
    return new Response(JSON.stringify({ version: bundleVersion() }), {
      headers: { "Content-Type": "application/json", ...CORS },
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
          const clientId = body.clientId ? String(body.clientId) : undefined
          saveDraftToDisk(text)
          // clientId lets the originating client ignore its own echo (draft
          // self-echo refilled a just-sent input on mobile)
          broadcastToClients("draft", { text, ...(clientId ? { clientId } : {}) })
          controller.enqueue(enc.encode(JSON.stringify({ ok: true })))
        } catch { controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  if (req.method === "POST" && pathname === "/api/chat/clear") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        let channel: string | undefined
        try { channel = String((await req.json()).channel ?? "").trim() || undefined } catch { /* empty body OK */ }
        const before = history.length
        if (channel) {
          const keep = history.filter(m => (m as Record<string,string>).agent !== channel)
          history.splice(0, history.length, ...keep)
        } else {
          history.splice(0, history.length)
        }
        rebuildHistoryIndex()
        try {
          const { writeFileSync } = require("fs") as typeof import("fs")
          const { HISTORY_FILE } = await import("./storage")
          writeFileSync(HISTORY_FILE, history.map(m => JSON.stringify(m)).join("\n") + (history.length ? "\n" : ""), "utf8")
        } catch { /* best-effort */ }
        broadcastToClients("reload", {})
        controller.enqueue(enc.encode(JSON.stringify({ ok: true, removed: before - history.length, remaining: history.length })))
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  if (req.method === "POST" && pathname === "/api/chat/reload") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        let device: string | undefined
        try { device = String((await req.json()).device ?? "").trim() || undefined } catch { /* empty body OK — back-compat */ }
        const clients = device ? broadcastToDevice(device, "reload", {}) : (broadcastToClients("reload", {}), sseClients.size)
        controller.enqueue(enc.encode(JSON.stringify({ ok: true, clients, ...(device ? { device } : {}) })))
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
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
          const device = String(body.device ?? "").trim() || undefined
          // device present → drive only that device; absent → global (back-compat)
          const clients = device
            ? broadcastToDevice(device, "navigate", { url, openDrawer })
            : (broadcastToClients("navigate", { url, openDrawer }), sseClients.size)
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, clients, url, openDrawer, ...(device ? { device } : {}) })))
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
      // A listening-but-silent agent gets a tab immediately — /reply upserts
      // the real name/color later.
      if (!agents.has(channel)) {
        const info = { id: channel, name: channel, color: "#6b7280" }
        agents.set(channel, info)
        broadcastToClients("agent_register", info)
        persistAgents()
      }
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
    // NOTE: every controller.enqueue/close is guarded — if the poll client
    // disconnects early (curl killed, monitor Ctrl-C'd), the stream is already
    // canceled and an unguarded enqueue in the timer is an uncaught TypeError
    // that kills the whole Pulse process (observed 2026-07-13, ~2min crash loop).
    let waiter: PollWaiter | null = null
    const removeWaiter = () => {
      if (!waiter) return
      const idx = pollWaiters.indexOf(waiter)
      if (idx !== -1) pollWaiters.splice(idx, 1)
      if (pollWaiters.length === 0) setAgentPresence(false)
    }
    return new Response(new ReadableStream({
      start(controller) {
        const enc = new TextEncoder()
        const timer = setTimeout(() => {
          removeWaiter()
          broadcastPresenceMap()
          try { controller.enqueue(enc.encode(JSON.stringify({ timeout: true }))); controller.close() } catch { /* client gone */ }
        }, 30_000)
        waiter = {
          resolve(msg) {
            try { controller.enqueue(enc.encode(JSON.stringify(msg))); controller.close() } catch { /* client gone */ }
          },
          timer,
          channel,
        }
        pollWaiters.push(waiter)
        setAgentPresence(true)
      },
      cancel() {
        // Client disconnected mid-poll — clean up so the waiter can't fire later
        if (waiter) clearTimeout(waiter.timer)
        removeWaiter()
        broadcastPresenceMap()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  return null
}
