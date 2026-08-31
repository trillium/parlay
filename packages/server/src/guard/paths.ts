// Which routes the guard applies to — see ./index.ts for the policy it
// applies to them, and for why this module exists at all.
//
// This is the part that rots. The mechanism in index.ts was correct the whole
// time; task-6ai1's D9 was entirely about routes that were never added here.

// ── Legacy wildcard CORS ────────────────────────────────────────────────────
// Still applied to the UNGUARDED routes — the read surface (history, version,
// pages, plugins manifest) and GET /api/chat/uploads/<name>, which serves
// content-addressed image bytes an <img> tag must be able to load. It lives
// here rather than in sse.ts because the CORS policy belongs with the code
// that decides who gets it. sse.ts re-exports this name so existing importers
// keep their `from "./sse"` path.
//
// Note /api/chat/history stays on this wildcard by prior decision: it hands
// out full chat bodies to any origin, which is a bigger disclosure than the
// identifiers this guard exists for — but it has always been the documented
// world-readable surface and closing it is a product decision, not a guard
// classification. Revisit deliberately, not as a rider on some other fix.
export const CORS = {
  "Access-Control-Allow-Origin":  "*",
  "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
  "Access-Control-Allow-Headers": "Content-Type",
}

// ── Guarded route set ───────────────────────────────────────────────────────
// THE RULE, and the exact boundary it describes. This set is the MUTATING and
// IDENTIFIER-AIMING surface: the routes that write server state, drive a
// device, or hand out an identifier the rest of the surface can then be aimed
// with. Within that surface, membership is decided by what the handler DOES,
// REGARDLESS OF HTTP METHOD. "It is a GET" is not evidence of anything —
// /poll is a GET that writes to the agent registry, and /subscribers is a GET
// that hands out the ids the rest of the surface is aimed with. Classify by
// reading the handler, never by the verb or by the route's name.
//
// The set below now covers the whole identifier surface. The two routes that
// were long tracked as accepted residue under
// `identifier-disclosure-remains-on-sse` — GET /api/chat/events (writes
// `sseClients` from an attacker-supplied `?device=`, and the tts_event frames
// its stream carries reach every connected client with that device uuid in
// them; router-tts-events.ts broadcasts { …, device, ...body } unfiltered)
// and GET /api/chat/agents (every registered agent id, previously under the
// wildcard CORS above — the same class of disclosure /subscribers was guarded
// for) — are guarded as of this change, closing that tracking item. Guarding,
// not Go's no-ACAO-ever: `parlay-input` (packages/input, published npm) is a
// legitimate cross-origin EventSource consumer with a configurable base URL,
// so these routes must keep a reflected ACAO for allowed origins (loopback /
// .local / private-LAN / PARLAY_ALLOWED_ORIGINS) while foreign pages get 403.
// Both are GETs, so the content-type gate never applies — this is purely the
// origin check plus the loss of the wildcard ACAO.
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
  // The former `identifier-disclosure-remains-on-sse` residue, now closed —
  // see the guarded-route-set comment above for what each discloses and why
  // reflected ACAO (not no-ACAO) is the right shape here.
  "/api/chat/events",
  "/api/chat/agents",
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
  "/api/chat/tts/validate-splits", // POST scores a split against a local model
                                  // — origin-guarded, see JSON_EXEMPT_PATHS

  // POST appends attacker-shapeable lines to a log file on disk
  // ($PARLAY_STATE_HOME/debug.log) — a state write, same class as
  // /tts-report. Its only in-repo caller (packages/client/src/debug-log.ts)
  // sends `Content-Type: application/json`, so the JSON gate costs it
  // nothing.
  "/api/chat/debug-log",

  // A GET that mutates. router-poll.ts registers an unknown `channel` on
  // first poll: it inserts into `agents`, broadcasts `agent_register` to
  // every SSE client, and calls persistAgents() — a registry write, an
  // event to the panel, and a disk write, from a cross-origin CORS-simple
  // GET needing no preflight. Guarding it costs no real caller: every
  // poller in this repo (the relay, the Go and TS CLI monitors,
  // tools/split-test, pages/chat/agent-notify.ts) is a no-Origin HTTP
  // client, and nothing in packages/client polls at all.
  "/api/chat/poll",
])

// Prefix matches.
//   /api/chat/agents/  — DELETE /api/chat/agents/:id, the REST alias for
//     unregister. The trailing slash keeps this a distinct match from
//     GET /api/chat/agents, which is guarded by its own exact-set entry above.
//   /api/chat/plugin/  — plugin RPC. Guarded as a PREFIX, deliberately, so a
//     plugin added later is guarded by default instead of shipping open until
//     someone remembers this file. Today that is the Cursorless bridge, whose
//     /rpc drives edits into the captain's input box. Its two routes do NOT
//     share a content-type contract. The panel's /response POST sets
//     `Content-Type: application/json` explicitly (packages/client/
//     src-plugins/cursorless.ts), so it keeps both layers. /rpc's caller is
//     Talon-side Python that lives outside this repo, and the handler reads it
//     with `await req.json()` (../plugins/cursorless.ts), which parses the
//     body as JSON whatever the header says — so /rpc's contract has always
//     been a JSON BODY, never a JSON content type, and it is in
//     JSON_EXEMPT_PATHS for that reason. Both routes stay origin-guarded. A
//     future plugin route whose callers do not send a JSON content type
//     belongs in JSON_EXEMPT_PATHS too.
//   /api/debug/    — input-timing telemetry. Not under /api/chat, so it is
//     dispatched in index.ts ahead of handleChatRequest and has to run the
//     guard itself; index.ts does. Guarded for the same reason /subscribers
//     is: its GET response is keyed BY DEVICE ID, so it is a second place a
//     foreign origin could read the identifier the D9 chain needs, and its
//     POST lets any origin write into the buffer.
const GUARDED_PREFIXES = ["/api/chat/agents/", "/api/chat/plugin/", "/api/debug/"]

// Guarded paths that must NOT be held to `Content-Type: application/json`.
// Three members, each for its own stated reason — this is a deliberate list,
// not a special case with an exception bolted on. All three stay INSIDE the
// guarded set above: the exemption drops one layer on one route, never the
// boundary. Membership was decided by one sweep of the whole guarded set
// against a three-part test — the handler parses the body regardless of
// Content-Type, the contract is JSON by SEMANTICS rather than by header, and
// no in-repo caller depends on the strict header contract — so this is a
// closed list rather than a queue that grows one route per bug report. Every
// other guarded route has an in-repo caller that already sends
// `Content-Type: application/json`, which is why the gate costs it nothing.
//
//   /api/chat/upload — multipart/form-data by contract (the panel posts a
//     FormData), so the gate would 415 every legitimate upload.
//   /api/chat/plugin/cursorless/rpc — its handler is `await req.json()`
//     (../plugins/cursorless.ts), which parses the body regardless of the
//     header, and its only caller is the out-of-repo Talon script. A Python
//     `requests.post(url, data=…)` sends a JSON body under
//     application/x-www-form-urlencoded, and that worked before this guard
//     existed; holding the route to the header would break voice editing at
//     runtime on the captain's box, with no caller in this repo to catch it.
//   /api/chat/tts/validate-splits — same shape as /rpc. Its handler is
//     `await req.json()` (../tts-validate.ts), which parses the body whatever
//     the header says; its documented contract (that file's header) is a
//     hand-run request stating a JSON BODY and no content type at all; and it
//     has zero callers anywhere in this repo, so the only callers that exist
//     are hand-typed `curl -d` — which defaults to
//     application/x-www-form-urlencoded — and out-of-repo scripts nothing here
//     would catch breaking. What the route DOES: it splits a supplied string
//     and asks a local Ollama model to score the split boundaries. It aims
//     nothing at the captain — unlike /rpc, which drives text into the
//     captain's input box — so the exemption is lower-risk here than on /rpc,
//     where it was already accepted.
//
// Escalated by that same sweep and DELIBERATELY LEFT UNEXEMPT — recorded so
// the next reader inherits the decision instead of re-litigating it one route
// per gate: /api/chat/system, /api/chat/clear, /api/chat/navigate,
// /api/chat/device-cmd and /api/chat/tts. All five read their body with
// `await req.json()`, so they clear the first leg of the test and nothing
// else.
//   - /api/chat/tts fails the caller leg: packages/client/src-plugins/speak/
//     cache.ts posts it with `Content-Type: application/json`, as do its tts
//     siblings (pinned by "the tts siblings are NOT exempt" in ./paths.test.ts),
//     so the gate costs it nothing.
//   - The other four have no in-repo caller — the shape /rpc and
//     /validate-splits were exempted on — but they were in the ORIGINAL guarded
//     set (review-3y3 / task-qg00), so their out-of-repo callers have been held
//     to this gate since it shipped with no breakage reported. There is no
//     unmet caller to exempt them for, and each one AIMS something at the
//     captain (a system line into the live thread, a history wipe, a panel
//     navigation, a device command), which is exactly the class the
//     content-type gate exists to keep out of no-preflight reach.
//
// For all three, the origin check alone is the defense, and it is sufficient:
// a browser always sends Origin on a cross-origin request — on a multipart
// form POST and on a simple-content-type POST alike — so a foreign page cannot
// reach any of these handlers. The content-type gate is defence-in-depth on
// the other paths (it forces a preflight, which is then refused), not the
// primary check.
export const JSON_EXEMPT_PATHS = new Set([
  "/api/chat/upload",
  "/api/chat/plugin/cursorless/rpc",
  "/api/chat/tts/validate-splits",
])

export function isGuardedChatPath(pathname: string): boolean {
  return GUARDED_CHAT_PATHS.has(pathname) || GUARDED_PREFIXES.some(p => pathname.startsWith(p))
}
