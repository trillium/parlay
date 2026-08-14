import { serve } from "bun"
import { handleChatRequest } from "./router"
import { handleDebugRequest } from "./router-debug"
import { guardChatRequest, isGuardedChatPath, preflightResponse, withGuardedCors } from "./guard"
import { loadHistory, loadDraftFromDisk, HISTORY_DIR } from "./storage"
import { watchPages } from "./pages"
import { broadcastToClients } from "./sse"
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
    // handleChatRequest may return a Response, a Promise<Response|null> (the
    // async server-side-eval routes), or null. await resolves the promise case;
    // a null (sync or resolved) falls through to 404.
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
    return resp ?? new Response("not found", { status: 404 })
  },
})
