import { serve } from "bun"
import { handleChatRequest } from "./router"
import { loadHistory, loadDraftFromDisk, HISTORY_DIR } from "./storage"
import { mkdirSync } from "fs"

const PORT     = Number(process.env.PARLAY_PORT ?? 4242)
const DATA_DIR = HISTORY_DIR

mkdirSync(DATA_DIR, { recursive: true })
console.log(`Parlay server  http://localhost:${PORT}`)
console.log(`Data dir       ${DATA_DIR}`)

loadHistory()
loadDraftFromDisk()

serve({
  port: PORT,
  async fetch(req) {
    const url = new URL(req.url)
    // handleChatRequest may return a Response, a Promise<Response|null> (the
    // async server-side-eval routes), or null. await resolves the promise case;
    // a null (sync or resolved) falls through to 404.
    const resp = await handleChatRequest(req, url.pathname)
    return resp ?? new Response("not found", { status: 404 })
  },
})
