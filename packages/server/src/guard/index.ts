// Origin + content-type hardening for the GUARDED chat routes (review-3y3 /
// task-qg00). Barrel for this folder: ./paths.ts decides WHICH routes are
// guarded — the mutating and identifier-aiming surface, classified by what the
// handler does rather than by its HTTP method, with the accepted residue named
// there — ./origin.ts decides WHO may call them, and this file applies the
// policy and builds the responses.
//
// The chat API has no authentication: anything that can reach the port can
// post into a live agent's turn. Before this module, EVERY /api/chat response
// carried `Access-Control-Allow-Origin: *` and OPTIONS was a blanket 204 — so
// any website the captain visited could POST /api/chat/send cross-site, with
// no network position, and have it land in a running crewmate's context.
//
// Two narrow defenses. Deliberately NOT a token/secret scheme — that is a
// follow-up; this folder is only the free half.
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
// Read routes (history, version, pages, uploads) are untouched and still
// world-readable. /agents and /events, once accepted residue tracked as
// `identifier-disclosure-remains-on-sse`, are now inside the guard — they
// disclose identifiers (agent ids; the device uuid on tts_event frames), so
// they get the origin check and reflected ACAO like /subscribers. See
// ./paths.ts's guarded-route-set comment.
//
// ── Second pass (task-6ai1, defect D9 of the end-to-end verification) ────────
// The guard itself was correct; the ROUTE SET was not. An end-to-end verifier
// chained the routes that were left outside it into full control of the panel:
// `GET /api/chat/subscribers` handed out a connected device uuid to any origin
// (wildcard ACAO), `POST /api/chat/eval` drove a real `input_action` into that
// device, `PUT /api/chat/draft` set the captain's outgoing text, and the
// engine's submit phrase sent it — attacker-authored text delivered to an
// agent AS THE CAPTAIN. Every route in that chain — and every other route that
// AIMS anything — is now inside the guard; see ./paths.ts for the route set,
// the classification test, and the two routes left outside it as accepted
// residue (also noted above).
//
// packages/go-server/internal/guard is the Go port of this policy (defect D7),
// and its package comment states the two places the two deliberately differ.

import { CORS, isGuardedChatPath, JSON_EXEMPT_PATHS } from "./paths"
import { isJsonContentType, originAllowed } from "./origin"

export { CORS, GUARDED_CHAT_PATHS, JSON_EXEMPT_PATHS, isGuardedChatPath } from "./paths"
export { allowedOriginList, isJsonContentType, originAllowed } from "./origin"

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
    // PUT is here for /api/chat/draft and /api/chat/parlay/settings, both of
    // which the panel writes with a same-origin PUT.
    "Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
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
  // body here, and GET has no body at all. JSON_EXEMPT_PATHS opts a guarded
  // route out of THIS GATE ONLY — never out of the origin check above — when
  // its callers do not send a JSON content type (a multipart upload; an
  // out-of-repo caller whose handler reads the body with req.json() regardless
  // of the header). See ./paths.ts for the per-route reason.
  if ((req.method === "POST" || req.method === "PUT")
      && !JSON_EXEMPT_PATHS.has(pathname)
      && !isJsonContentType(req.headers.get("content-type"))) {
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
