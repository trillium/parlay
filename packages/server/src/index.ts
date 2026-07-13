import { serve } from "bun"
import { handleChatRequest } from "./router"
import { loadHistory, loadDraftFromDisk } from "./storage"
import { join } from "path"
import { homedir } from "os"
import { mkdirSync } from "fs"

const PORT     = Number(process.env.PARLAY_PORT ?? 4242)
const DATA_DIR = process.env.PARLAY_DATA_DIR ?? join(homedir(), ".parlay")

mkdirSync(DATA_DIR, { recursive: true })
console.log(`Parlay server  http://localhost:${PORT}`)
console.log(`Data dir       ${DATA_DIR}`)

loadHistory()
loadDraftFromDisk()

serve({
  port: PORT,
  fetch(req) {
    const url = new URL(req.url)
    return handleChatRequest(req, url.pathname) ?? new Response("not found", { status: 404 })
  },
})
