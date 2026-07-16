import { readdirSync, statSync, readFileSync, watch } from "fs"
import { join } from "path"
import { homedir } from "os"
import { CORS } from "./sse"

// ── Local Pulse page index ────────────────────────────────────────────────────
// GET /api/chat/pages → the servable pages under ~/pulse-pages/: every directory
// holding an index.html, with its <title> for fuzzy search. Powers the panel's
// page-nav picker (navigate the workspace to /<tag>/). Cheap + cached; the page
// set changes rarely, so a 30s TTL keeps the readdir off the hot path.

const PAGES_ROOT = join(homedir(), "pulse-pages")
const TTL_MS = 30_000

interface PageEntry { tag: string; title: string }

let cache: { at: number; pages: PageEntry[] } | null = null

function titleOf(indexPath: string, fallback: string): string {
  try {
    const head = readFileSync(indexPath, "utf8").slice(0, 4096)
    const m = head.match(/<title>([^<]*)<\/title>/i)
    const t = m?.[1]?.replace(/\s+/g, " ").trim()
    return t && t.length ? t : fallback
  } catch {
    return fallback
  }
}

function listPages(): PageEntry[] {
  const out: PageEntry[] = []
  let names: string[]
  try { names = readdirSync(PAGES_ROOT) } catch { return out }
  for (const name of names) {
    if (name.startsWith(".")) continue
    const idx = join(PAGES_ROOT, name, "index.html")
    try { if (!statSync(idx).isFile()) continue } catch { continue }
    out.push({ tag: name, title: titleOf(idx, name) })
  }
  out.sort((a, b) => a.tag.localeCompare(b.tag))
  return out
}

export function handlePagesRequest(req: Request, pathname: string): Response | null {
  if (pathname !== "/api/chat/pages") return null
  if (req.method !== "GET") return json({ error: "GET only" }, 405)
  if (!cache || Date.now() - cache.at > TTL_MS) cache = { at: Date.now(), pages: listPages() }
  return json({ pages: cache.pages }, 200)
}

// Watch ~/pulse-pages/ for new or removed pages and broadcast a PATCH to all
// connected clients — eliminates polling; clients apply the diff in-place.
export function watchPages(broadcast: (event: string, data: unknown) => void): void {
  let debounce: ReturnType<typeof setTimeout> | null = null
  try {
    watch(PAGES_ROOT, { persistent: false }, () => {
      if (debounce) clearTimeout(debounce)
      debounce = setTimeout(() => {
        debounce = null
        const fresh = listPages()
        const prev = cache?.pages ?? []
        const prevTags = new Set(prev.map(p => p.tag))
        const freshTags = new Set(fresh.map(p => p.tag))
        const added = fresh.filter(p => !prevTags.has(p.tag))
        const removed = prev.filter(p => !freshTags.has(p.tag)).map(p => p.tag)
        if (added.length || removed.length) {
          cache = { at: Date.now(), pages: fresh }
          broadcast("pages_patch", { added, removed })
        }
      }, 500)
    })
  } catch { /* ~/pulse-pages may not exist at startup */ }
}

function json(data: unknown, status: number): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json", ...CORS },
  })
}
