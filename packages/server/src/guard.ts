// Origin + content-type hardening for the MUTATING chat routes (review-3y3 /
// task-qg00).
//
// The chat API has no authentication: anything that can reach the port can
// post into a live agent's turn. Before this module, EVERY /api/chat response
// carried `Access-Control-Allow-Origin: *` and OPTIONS was a blanket 204 — so
// any website the captain visited could POST /api/chat/send cross-site, with
// no network position, and have it land in a running crewmate's context.
//
// Two narrow defenses. Deliberately NOT a token/secret scheme — that is a
// follow-up; this file is only the free half.
//
//   1. Origin allow-list on the mutating routes. A request with NO Origin
//      header is ALLOWED: that is the CLI, curl, hooks and every
//      server-to-server caller, and a browser cannot forge that absence on a
//      cross-site request. A request WITH an Origin must be same-origin (or
//      loopback / private-LAN / env allow-listed); anything else gets 403 and
//      no CORS headers at all.
//   2. `Content-Type: application/json` required on the mutating POSTs, 415
//      otherwise. This is the load-bearing one: it means a cross-origin POST
//      can no longer be a CORS "simple request" (whose content types are only
//      text/plain, application/x-www-form-urlencoded, multipart/form-data, or
//      absent via an empty-type Blob). The browser must preflight — and
//      preflight on these paths is rejected. The no-preflight shapes cannot
//      reach the handler at all.
//
// Read routes (history, agents, SSE events, poll) are untouched and still
// world-readable: out of scope here, see the PR body.

// ── Legacy wildcard CORS ────────────────────────────────────────────────────
// Still applied to the UNGUARDED routes (read/SSE/upload/tts/eval), which is
// why it lives here rather than in sse.ts — the CORS policy belongs with the
// code that decides who gets it. sse.ts re-exports this name so existing
// importers keep their `from "./sse"` path.
export const CORS = {
  "Access-Control-Allow-Origin":  "*",
  "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
  "Access-Control-Allow-Headers": "Content-Type",
}

// ── Guarded route set ───────────────────────────────────────────────────────
// Everything that injects into an agent turn, mutates the registry, or drives
// a connected device. /api/chat/upload is intentionally absent (it is
// multipart by contract); so are the read routes and the SSE stream.
export const GUARDED_CHAT_PATHS = new Set([
  "/api/chat/send",
  "/api/chat/reply",
  "/api/chat/alert",
  "/api/chat/system",
  "/api/chat/register-agent",
  "/api/chat/unregister",
  "/api/chat/declare-channel",
  "/api/chat/clear",
  "/api/chat/navigate",
  "/api/chat/reload",
  "/api/chat/device-cmd",
])

// DELETE /api/chat/agents/:id — the REST alias for unregister. Note the
// trailing slash: GET /api/chat/agents (the read route) is NOT matched.
const GUARDED_PREFIXES = ["/api/chat/agents/"]

export function isGuardedChatPath(pathname: string): boolean {
  return GUARDED_CHAT_PATHS.has(pathname) || GUARDED_PREFIXES.some(p => pathname.startsWith(p))
}

// ── Origin policy ───────────────────────────────────────────────────────────

// Loopback and private-LAN literals. The phone reaches the panel over the LAN
// (Origin http://192.168.x.x:31337) and Pulse may reverse-proxy /api/chat/* to
// this server under a rewritten Host, so a strict same-host test alone would
// cut off legitimate local clients. None of these can be an attacker's origin
// without them already serving pages from inside the captain's network — and
// DNS rebinding does not help, since the Origin header keeps the attacker's
// own name.
const PRIVATE_V4 = /^(10\.|127\.|169\.254\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/

function isLocalHostname(hostname: string): boolean {
  const h = hostname.replace(/^\[|\]$/g, "").toLowerCase()
  if (h === "localhost" || h.endsWith(".localhost")) return true
  if (h.endsWith(".local")) return true
  if (h === "::1" || h === "0:0:0:0:0:0:0:1") return true
  return PRIVATE_V4.test(h)
}

// PARLAY_ALLOWED_ORIGINS: comma-separated exact origins (e.g. a tunnel
// hostname). "*" opts out of the origin check entirely — an escape hatch for
// a deployment that needs it, never the default.
export function allowedOriginList(): string[] {
  return (process.env.PARLAY_ALLOWED_ORIGINS ?? "").split(",").map(s => s.trim()).filter(Boolean)
}

export function originAllowed(req: Request): boolean {
  const origin = req.headers.get("origin")
  // No Origin at all → not a browser cross-site request. CLI, curl, hooks,
  // MuseFeeder, the relay. Allowing this is what keeps the live fleet working.
  if (!origin) return true

  const list = allowedOriginList()
  if (list.includes("*")) return true
  if (list.includes(origin)) return true

  // "null" is what a sandboxed iframe / file:// / redirected request sends.
  if (origin === "null") return false

  let u: URL
  try { u = new URL(origin) } catch { return false }
  if (u.protocol !== "http:" && u.protocol !== "https:") return false

  // Same-origin: the Origin's host:port matches the Host this request arrived
  // on. Covers localhost:31337, the LAN IP, and any tunnel that forwards Host.
  const host = req.headers.get("host") ?? safeUrlHost(req.url)
  if (host && u.host.toLowerCase() === host.toLowerCase()) return true

  return isLocalHostname(u.hostname)
}

function safeUrlHost(url: string): string {
  try { return new URL(url).host } catch { return "" }
}

// ── Content type ────────────────────────────────────────────────────────────

export function isJsonContentType(value: string | null): boolean {
  if (!value) return false
  return value.split(";")[0]!.trim().toLowerCase() === "application/json"
}

// ── Responses ───────────────────────────────────────────────────────────────

// A rejection carries NO Access-Control-Allow-Origin — the calling page must
// not be able to read the outcome either.
function deny(status: number, error: string): Response {
  return new Response(JSON.stringify({ error }), {
    status,
    headers: { "Content-Type": "application/json", "Vary": "Origin" },
  })
}

// CORS headers for a guarded route: never a wildcard. Reflect the single
// allowed origin so the same-origin panel can still read its own responses,
// and Vary so a shared cache cannot hand one origin's ACAO to another.
export function guardedCorsHeaders(req: Request): Record<string, string> {
  const origin = req.headers.get("origin")
  if (!origin || !originAllowed(req)) return { "Vary": "Origin" }
  return {
    "Access-Control-Allow-Origin":  origin,
    "Access-Control-Allow-Methods": "GET, POST, DELETE, OPTIONS",
    "Access-Control-Allow-Headers": "Content-Type",
    "Vary": "Origin",
  }
}

// Strip whatever CORS a handler set (they all spread the wildcard `CORS`) and
// replace it with the reflected-origin form.
export function withGuardedCors(req: Request, resp: Response): Response {
  const headers = new Headers(resp.headers)
  headers.delete("Access-Control-Allow-Origin")
  headers.delete("Access-Control-Allow-Methods")
  headers.delete("Access-Control-Allow-Headers")
  for (const [k, v] of Object.entries(guardedCorsHeaders(req))) headers.set(k, v)
  return new Response(resp.body, { status: resp.status, statusText: resp.statusText, headers })
}

// Returns a rejection Response, or null to let the request through.
export function guardChatRequest(req: Request, pathname: string): Response | null {
  if (!isGuardedChatPath(pathname)) return null
  if (req.method === "OPTIONS") return null // preflightResponse owns OPTIONS
  if (!originAllowed(req)) return deny(403, "cross-origin request rejected")
  // Only POST/PUT can arrive without a preflight, so only they need the
  // content-type gate; DELETE is never a CORS simple request and carries no
  // body here.
  if ((req.method === "POST" || req.method === "PUT") && !isJsonContentType(req.headers.get("content-type"))) {
    return deny(415, "Content-Type: application/json required")
  }
  return null
}

// OPTIONS handling. Guarded paths get a real preflight decision instead of the
// old blanket 204; everything else keeps the previous permissive behavior.
export function preflightResponse(req: Request, pathname: string): Response {
  if (!isGuardedChatPath(pathname)) return new Response(null, { status: 204, headers: CORS })
  if (!originAllowed(req)) return deny(403, "cross-origin preflight rejected")
  return new Response(null, {
    status: 204,
    headers: { ...guardedCorsHeaders(req), "Access-Control-Max-Age": "600" },
  })
}
