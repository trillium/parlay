import { createHash } from "node:crypto"
import { mkdirSync, writeFileSync, readFileSync, existsSync } from "node:fs"
import { join } from "node:path"
import { CORS } from "./sse"

// ── Image uploads (#17) ─────────────────────────────────────────────────────
// POST /api/chat/upload (multipart, field "file") → saves to
// ~/exchange/parlay-uploads/<sha1-12>.<ext> (captain-specified location) and
// returns {ok, url}. GET /api/chat/uploads/<name> serves the file INLINE with
// a real image content-type — the portal exchange route forces attachment
// disposition, which is wrong for <img>/new-tab viewing. Images only, ≤10MB.
//
// ── AGENT CONTRACT: URL → filesystem mapping (canonical, #17 addendum) ──────
// Every image URL of the form
//     /api/chat/uploads/<name>          (or http://<host>:31337/api/chat/uploads/<name>)
// maps to the on-disk file
//     ~/exchange/parlay-uploads/<name>
// Names are content-addressed (sha1-12 + ext) and the location is stable.
// Agents: Read that filesystem path directly — the Read tool renders images
// for vision. Implement this one mapping identically everywhere.

const UPLOAD_DIR = join(process.env.HOME ?? "", "exchange", "parlay-uploads")
const MAX_BYTES = 10 * 1024 * 1024

const EXT_TYPES: Record<string, string> = {
  png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg",
  gif: "image/gif", webp: "image/webp", svg: "image/svg+xml",
}

function extFor(file: File): string | null {
  const byMime = Object.entries(EXT_TYPES).find(([, t]) => t === file.type)?.[0]
  if (byMime) return byMime === "jpeg" ? "jpg" : byMime
  const m = (file.name ?? "").toLowerCase().match(/\.(png|jpe?g|gif|webp|svg)$/)
  return m ? (m[1] === "jpeg" ? "jpg" : m[1]) : null
}

export function handleUploadRequest(req: Request, pathname: string): Response | null {
  if (req.method === "GET" && pathname.startsWith("/api/chat/uploads/")) {
    const name = pathname.slice("/api/chat/uploads/".length)
    if (!/^[a-z0-9]+\.(png|jpg|gif|webp|svg)$/.test(name)) {
      return new Response("bad name", { status: 400, headers: CORS })
    }
    const path = join(UPLOAD_DIR, name)
    if (!existsSync(path)) return new Response("not found", { status: 404, headers: CORS })
    const ext = name.split(".").pop()!
    return new Response(readFileSync(path), {
      headers: {
        "Content-Type": EXT_TYPES[ext] ?? "application/octet-stream",
        "Content-Disposition": "inline",
        "Cache-Control": "public, max-age=31536000, immutable",   // content-addressed
        ...CORS,
      },
    })
  }

  if (req.method === "POST" && pathname === "/api/chat/upload") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        const reply = (obj: unknown) => { controller.enqueue(enc.encode(JSON.stringify(obj))); controller.close() }
        try {
          const form = await req.formData()
          const file = form.get("file")
          if (!(file instanceof File)) { reply({ error: "file field required" }); return }
          if (file.size > MAX_BYTES) { reply({ error: "too large (10MB max)" }); return }
          const ext = extFor(file)
          if (!ext) { reply({ error: "images only (png/jpg/gif/webp/svg)" }); return }
          const bytes = new Uint8Array(await file.arrayBuffer())
          const name = createHash("sha1").update(bytes).digest("hex").slice(0, 12) + "." + ext
          mkdirSync(UPLOAD_DIR, { recursive: true })
          writeFileSync(join(UPLOAD_DIR, name), bytes)
          reply({ ok: true, url: `/api/chat/uploads/${name}`, bytes: bytes.length })
        } catch { reply({ error: "bad request" }) }
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  return null
}
