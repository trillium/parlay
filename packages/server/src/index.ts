import { serve } from "bun"
import { handleChatRequest } from "./router"
import { handleDebugRequest } from "./router-debug"
import { handleParlayUiRequest } from "./parlay-ui"
import { handleStaticRequest } from "./static"
import { guardChatRequest, isGuardedChatPath, preflightResponse, withGuardedCors } from "./guard"
import { loadHistory, loadDraftFromDisk, HISTORY_DIR, history } from "./storage"
import { watchPages } from "./pages"
import { agents, broadcastToClients } from "./sse"
import { startHookFiringTailer } from "./hook-tailer"
import { startToolEventTailer } from "./tool-tailer"
import { startPruneSweeps } from "./prune"
import { backfillFromToolActivity } from "./session-channel"
import { mkdirSync } from "fs"

const PORT     = Number(process.env.PARLAY_PORT ?? 4242)
const DATA_DIR = HISTORY_DIR

mkdirSync(DATA_DIR, { recursive: true })
console.log(`Parlay server  http://localhost:${PORT}`)
console.log(`Data dir       ${DATA_DIR}`)

loadHistory()
loadDraftFromDisk()
watchPages(broadcastToClients)
startHookFiringTailer()
backfillFromToolActivity()
startToolEventTailer()
startPruneSweeps()

serve({
  port: PORT,
  async fetch(req) {
    const url = new URL(req.url)
    // Liveness + store sanity, outside /api/chat — the same route and shape
    // as the Go server's /health (docs/api-contract.md).
    if (url.pathname === "/health") {
      if (req.method !== "GET") return new Response("method not allowed", { status: 405 })
      return Response.json({ ok: true, messages: history.length, agents: agents.size })
    }
    // The embeddable panel loader at its documented top-level path
    // (docs/api-contract.md: GET /parlay-ui.js is NOT under /api/chat; the
    // /api/chat alias stays served via router.ts).
    if (url.pathname === "/parlay-ui.js") {
      const ui = handleParlayUiRequest(req, url.pathname)
      if (ui) return ui
    }
    // handleChatRequest may return a Response, a Promise<Response|null> (the
    // async server-side-eval routes), or null. await resolves the promise case;
    // a null (sync or resolved) falls through to the static bundle, then 404.
    // /api/debug/* is dispatched here, ahead of handleChatRequest, so it never
    // crosses router.ts's guard boundary — it has to run the guard itself.
    // GET /api/debug/input-timing is keyed by device id, the same identifier
    // /subscribers was leaking (task-6ai1 / D9).
    if (url.pathname.startsWith("/api/debug/")) {
      if (req.method === "OPTIONS") return preflightResponse(req, url.pathname)
      const denied = guardChatRequest(req, url.pathname)
      if (denied) return denied
      const dbg = handleDebugRequest(req, url.pathname)
      if (dbg) return isGuardedChatPath(url.pathname) ? withGuardedCors(req, dbg) : dbg
    }
    const resp = await handleChatRequest(req, url.pathname)
    if (resp) return resp
    // Static bundle catch-all, dispatched LAST so it can never shadow an API
    // route — and handleStaticRequest itself returns null for every /api/*
    // path, so an unrouted API path stays a real 404 (commandreport caches
    // those; an SPA fallback there would read as "verb exists").
    return handleStaticRequest(req, url.pathname) ?? new Response("not found", { status: 404 })
  },
})
