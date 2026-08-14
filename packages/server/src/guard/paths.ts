// Which routes the guard applies to — see ./index.ts for the policy it
// applies to them, and for why this module exists at all.
//
// This is the part that rots. The mechanism in index.ts was correct the whole
// time; task-6ai1's D9 was entirely about routes that were never added here.

// ── Legacy wildcard CORS ────────────────────────────────────────────────────
// Still applied to the UNGUARDED routes — the read/SSE surface (history,
// agents, poll, events, version, pages, plugins manifest) and GET
// /api/chat/uploads/<name>, which serves content-addressed image bytes an
// <img> tag must be able to load. It lives here rather than in sse.ts because
// the CORS policy belongs with the code that decides who gets it. sse.ts
// re-exports this name so existing importers keep their `from "./sse"` path.
export const CORS = {
  "Access-Control-Allow-Origin":  "*",
  "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
  "Access-Control-Allow-Headers": "Content-Type",
}

// ── Guarded route set ───────────────────────────────────────────────────────
// Everything that injects into an agent turn, mutates persisted state, drives
// a connected device, or discloses an identifier a cross-origin page could
// then aim at one of the above. Only the read/SSE surface is left outside.
export const GUARDED_CHAT_PATHS = new Set([
  // Agent-turn injection and registry mutation (original set).
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

  // D9, the proven chain. /eval relays into the compiled engine and
  // broadcasts the returned actions to a device as `input_action` — it is a
  // device-driving route, not a query. /eval-push is the same broadcast with
  // only a streamId, so it is guarded identically (the Go engine calls it
  // server-to-server with no Origin, which the guard allows).
  "/api/chat/eval",
  "/api/chat/eval-push",
  // GET and PUT alike: PUT sets the captain's outgoing text (the second half
  // of the chain), and GET reads it back — the draft is whatever the captain
  // is mid-way through typing, which no foreign origin should see either.
  "/api/chat/draft",
  // Read-only, but it is the route that handed the attack its device uuid and
  // the ids of every registered agent. GET carries no body, so the
  // content-type gate never applies to it; this is purely the origin check
  // plus the loss of the wildcard ACAO.
  "/api/chat/subscribers",
  // Multipart by contract — see JSON_EXEMPT_PATHS.
  "/api/chat/upload",

  // Not in the verification report, found while auditing the rest of the
  // route table for this fix. All four classes are the same defect as D9:
  // mutating or device-driving routes that sat outside the guard.
  "/api/chat/parlay/settings",   // PUT rewrites persisted panel/voice settings
  "/api/chat/tts",               // POST → the speak daemon: makes the host talk
  "/api/chat/tts-correction",    // POST persists a pronunciation override
  "/api/chat/tts-report",        // POST appends to the TTS report log
  "/api/chat/tts-event",         // POST broadcasts a tts_event to every client
  "/api/chat/tts/validate-splits",
])

// Prefix matches.
//   /api/chat/agents/  — DELETE /api/chat/agents/:id, the REST alias for
//     unregister. Note the trailing slash: GET /api/chat/agents (the read
//     route) is NOT matched.
//   /api/chat/plugin/  — plugin RPC. Guarded as a PREFIX, deliberately, so a
//     plugin added later is guarded by default instead of shipping open until
//     someone remembers this file. Today that is the Cursorless bridge, whose
//     /rpc drives edits into the captain's input box. Both its routes are
//     same-origin (panel) or no-Origin (Talon) JSON POSTs. A future plugin
//     route that is NOT JSON must be added to JSON_EXEMPT_PATHS.
//   /api/debug/    — input-timing telemetry. Not under /api/chat, so it is
//     dispatched in index.ts ahead of handleChatRequest and has to run the
//     guard itself; index.ts does. Guarded for the same reason /subscribers
//     is: its GET response is keyed BY DEVICE ID, so it is a second place a
//     foreign origin could read the identifier the D9 chain needs, and its
//     POST lets any origin write into the buffer.
const GUARDED_PREFIXES = ["/api/chat/agents/", "/api/chat/plugin/", "/api/debug/"]

// Guarded paths that must NOT be held to `Content-Type: application/json`.
// /api/chat/upload is multipart/form-data by contract — the panel posts a
// FormData — so the content-type gate would 415 every legitimate upload. For
// these, the origin check alone is the defense, and it is sufficient: a
// browser always sends Origin on a cross-origin request, including on a
// multipart form POST, so a foreign page cannot reach the handler. The
// content-type gate is belt-and-suspenders for the other paths (it forces a
// preflight, which is then refused), not the primary check.
export const JSON_EXEMPT_PATHS = new Set(["/api/chat/upload"])

export function isGuardedChatPath(pathname: string): boolean {
  return GUARDED_CHAT_PATHS.has(pathname) || GUARDED_PREFIXES.some(p => pathname.startsWith(p))
}
