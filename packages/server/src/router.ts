import { rebuildHistoryIndex, currentDraft, saveDraftToDisk } from "./storage"
import { history } from "./storage"
import { sseClients, CORS, broadcastToClients, broadcastToDevice } from "./sse"
import { handleMessagesRequest } from "./router-messages"
import { handleEventsRequest } from "./router-events"
import { handlePollRequest } from "./router-poll"
import { handleParlaySettings } from "./parlay-settings"
import { handleTTSRequest } from "./tts"
import { handleUploadRequest } from "./uploads"
import { handlePluginsRequest } from "./plugins"
import { handlePagesRequest } from "./pages"
import { handleParlayUiRequest } from "./parlay-ui"
import { handleEvalRequest } from "./eval-relay"
import { handleTTSValidateRequest } from "./tts-validate"
import { handleTtsEventRequest } from "./router-tts-events"

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

  // Shared UI utilities — syntax highlight, __paRegisterInput, __paPageId
  const uiResp = handleParlayUiRequest(req, pathname)
  if (uiResp !== null) return uiResp

  // Local page index: GET /api/chat/pages → pulse-pages for the nav picker
  const pagesResp = handlePagesRequest(req, pathname)
  if (pagesResp !== null) return pagesResp

  // Delegate message-centric routes (send, reply, register-agent, agents)
  const msgResp = handleMessagesRequest(req, pathname)
  if (msgResp !== null) return msgResp

  // TTS split quality validation (local Ollama)
  const ttsValidResp = handleTTSValidateRequest(req, pathname)
  if (ttsValidResp !== null) return ttsValidResp

  // TTS lifecycle event stream — broadcasts to SSE clients as tts_event
  const ttsEventResp = handleTtsEventRequest(req, pathname)
  if (ttsEventResp !== null) return ttsEventResp

  // SSE events stream, bundle version, history — router-events.ts
  const eventsResp = handleEventsRequest(req, pathname)
  if (eventsResp !== null) return eventsResp

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
          // A message's owning agent is its `channel` field (types.ts); there is
          // no `agent` field. The old `(m as Record<string,string>).agent` read a
          // nonexistent property → always undefined → channel-scoped clear was a
          // silent no-op. Match on the real field so clearing one channel works.
          const keep = history.filter(m => m.channel !== channel)
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

  // Agent long-poll — router-poll.ts
  const pollResp = handlePollRequest(req, pathname)
  if (pollResp !== null) return pollResp

  return null
}
