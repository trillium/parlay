import { CORS } from "./sse"
import { handlePluginRequest as cursorlessHandler, manifest as cursorlessManifest } from "./plugins/cursorless"

// ── Plugin registry (server side) ────────────────────────────────────────────
// Each plugin under modules/chat/plugins/ exports handlePluginRequest + a
// manifest. Static imports keep this deterministic; adding a plugin = one
// import + one entry here (+ Pulse restart). The panel loads client halves
// from /annotate/plugins/<id>.js based on GET /api/chat/plugins.

const handlers = [cursorlessHandler]
const manifests = [
  // speak loads FIRST — it wires the global speech hooks the thread transport uses
  {
    id: "speak",
    version: "1.0.0",
    minPanel: "3.7.0",
    description: "Kokoro speech playback, readiness dots, pronunciation corrector",
    defaultEnabled: true,
  },
  cursorlessManifest,
]

export function handlePluginsRequest(req: Request, pathname: string): Response | null {
  if (req.method === "GET" && pathname === "/api/chat/plugins") {
    return new Response(JSON.stringify(manifests), {
      headers: { "Content-Type": "application/json", ...CORS },
    })
  }
  if (pathname.startsWith("/api/chat/plugin/")) {
    for (const h of handlers) {
      const resp = h(req, pathname)
      if (resp !== null) return resp
    }
  }
  return null
}
