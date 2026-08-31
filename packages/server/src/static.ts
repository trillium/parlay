// Serves the built packages/client/dist bundle from this server, so the
// panel loads same-origin with no Pulse front door. Port of the Go server's
// internal/static package — keep the two in behavioral lockstep.
//
// Routes served (GET/HEAD only; dispatched AFTER all /api/* routes):
//
//   GET /                → dist/index.html
//   GET /parlay-agent.js → dist/parlay-agent.js  (same origin as the panel)
//   GET /annotate/<path> → dist/<path>            (Pulse-compat alias)
//   GET /fleet/<path>    → dist/fleet/<path>      (webview dashboard)
//   GET /<anything-else> → dist/index.html        (SPA fallback)
//
// The /annotate/ prefix matches the Pulse symlink convention
// (~/pulse-pages/annotate → packages/client), so any page that already
// loads <script src="/annotate/pulse-agent.js"> keeps working unchanged
// while this server is serving the panel.
//
// /api/* is deliberately EXCLUDED — an unrouted API path must stay a real
// 404, never a 200 text/html fallback: the CLI's commandreport probes
// unknown verbs and caches a genuine 404 per server (see
// docs/agent-notes/commandreport-caches-a-404-on-disk.md); an SPA fallback
// there would read as "verb exists".

import { statSync } from "fs"
import { join, resolve, sep } from "path"

// Same knob as the Go server (--assets-dir / PARLAY_ASSETS_DIR there); the
// coded default resolves the sibling client package so a repo checkout
// serves its own build from any cwd.
export const ASSETS_DIR =
  process.env.PARLAY_ASSETS_DIR ?? resolve(import.meta.dir, "..", "..", "client", "dist")

function fileResponse(path: string, head: boolean): Response {
  const f = Bun.file(path)
  // Explicit Content-Type: Bun only materializes the inferred type when a
  // Response(BunFile) is actually sent, so callers (and tests) inspecting
  // the header would otherwise see nothing.
  const headers = { "Content-Type": f.type }
  return head ? new Response(null, { headers }) : new Response(f, { headers })
}

function isFile(path: string): boolean {
  try { return statSync(path).isFile() } catch { return false }
}

// Serves dir+path when it is a real file, else falls back to dir/index.html
// (SPA routing — the panel loads on any URL the user bookmarks or refreshes).
function serveOrFallback(dir: string, path: string, head: boolean): Response {
  const candidate = resolve(dir, "." + join("/", path))

  // Prevent path traversal outside dir (join("/", …) above already collapses
  // any ../ inside the URL path; this is belt-and-braces, matching the Go
  // port's explicit check).
  if (candidate !== dir && !candidate.startsWith(dir + sep)) {
    return new Response("forbidden", { status: 403 })
  }

  if (isFile(candidate)) return fileResponse(candidate, head)

  const index = join(dir, "index.html")
  if (!isFile(index)) {
    return new Response(
      "index.html not found in assets directory — run `bun build.ts` in packages/client",
      { status: 404 },
    )
  }
  return fileResponse(index, head)
}

/**
 * Static-bundle dispatch. Returns null for paths this module does not own
 * (all of /api/*, non-GET/HEAD methods) so the caller's 404 stands.
 */
export function handleStaticRequest(
  req: Request,
  pathname: string,
  dir: string = ASSETS_DIR,
): Response | null {
  if (pathname.startsWith("/api/")) return null
  if (req.method !== "GET" && req.method !== "HEAD") return null
  const head = req.method === "HEAD"

  let path: string
  try { path = decodeURIComponent(pathname) } catch { return new Response("bad path", { status: 400 }) }

  if (!dir) {
    return new Response("no assets directory configured (set PARLAY_ASSETS_DIR)", { status: 503 })
  }
  try { statSync(dir) } catch {
    return new Response("assets directory not found: " + dir, { status: 503 })
  }

  // /fleet/<rest> → serve the webview dashboard from its own subtree, with
  // its own index.html fallback (mirrors the Go server's /fleet/ mount).
  if (path.startsWith("/fleet/")) {
    return serveOrFallback(join(dir, "fleet"), path.slice("/fleet".length), head)
  }

  // /annotate/<rest> → serve <rest> from the bundle directory, matching the
  // Pulse symlink convention so existing script tags work unchanged.
  if (path.startsWith("/annotate/")) {
    return serveOrFallback(dir, path.slice("/annotate".length), head)
  }

  return serveOrFallback(dir, path, head)
}
