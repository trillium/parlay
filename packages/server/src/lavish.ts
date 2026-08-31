import { watch } from "fs"
import { readFile, unlink } from "fs/promises"
import { homedir } from "os"
import { join } from "path"
import { broadcastToClients, agents } from "./sse"
import { addMessage } from "./router-messages"

const STATE_FILE  = join(homedir(), ".lavish-axi", "state.json")
const CLAIM_FILE  = join(homedir(), ".lavish-axi", "pending-claim.json")
const LAVISH_AGENT = { id: "lavish", name: "Lavish", color: "#f4c95d" }

// sessionKey → agentId: set when an agent claims a session via POST /api/lavish/claim
// or automatically from the pending-claim.json file written by the lavish wrapper
export const sessionOwners = new Map<string, string>()

async function consumePendingClaim(key: string) {
  try {
    const raw = await readFile(CLAIM_FILE, "utf8")
    const { agentId } = JSON.parse(raw)
    if (agentId) {
      sessionOwners.set(key, agentId)
      await unlink(CLAIM_FILE).catch(() => {})
    }
  } catch { /* no pending claim — that's fine */ }
}

interface LavishPrompt { text: string; tag?: string }
interface LavishSession {
  key: string; file: string; url: string; status: "open" | "ended"
  prompts: LavishPrompt[]
  chat: Array<{ role: string; text: string; at: string }>
}
interface Memo { status: string; promptCount: number }
const memo: Record<string, Memo> = {}

let agentRegistered = false
function ensureAgent() {
  if (agentRegistered) return
  agentRegistered = true
  agents.set("lavish", LAVISH_AGENT)
  broadcastToClients("agent_register", LAVISH_AGENT)
}

function fileName(p: string) { return p.split("/").pop() ?? p }
// Self-referential: the proxy route lives on this server (Pulse used to
// front it on :31337; off-Pulse the panel talks straight to us).
const SELF_PORT = Number(process.env.PARLAY_PORT ?? 4242)
function proxyUrl(key: string) { return `http://127.0.0.1:${SELF_PORT}/lavish-proxy/session/${key}` }

async function readState(): Promise<Record<string, LavishSession> | null> {
  try { return JSON.parse(await readFile(STATE_FILE, "utf8")).sessions }
  catch { return null }
}

async function diff() {
  const sessions = await readState()
  if (!sessions) return
  for (const [key, s] of Object.entries(sessions)) {
    const m = memo[key]
    if (!m) {
      memo[key] = { status: s.status, promptCount: s.prompts.length }
      if (s.status === "open") {
        await consumePendingClaim(key)   // auto-assign owner from wrapper's pending-claim.json
        ensureAgent()
        addMessage("agent", `Lavish artifact: ${fileName(s.file)} — ${proxyUrl(key)}`, "lavish")
        broadcastToClients("lavish_session", { key, file: s.file, proxyUrl: proxyUrl(key), status: "open" })
      }
      continue
    }
    if (m.status !== "ended" && s.status === "ended") {
      memo[key].status = "ended"
      addMessage("agent", `Lavish closed: ${fileName(s.file)}`, "lavish")
      broadcastToClients("lavish_session", { key, file: s.file, proxyUrl: proxyUrl(key), status: "ended" })
    }
    const fresh = s.prompts.slice(m.promptCount)
    if (fresh.length) {
      memo[key].promptCount = s.prompts.length
      for (const p of fresh) addMessage("user", `[annotation] ${p.text}`, "lavish")
    }
  }
}

export function startLavishWatcher() {
  diff()
  try {
    watch(STATE_FILE, { persistent: false }, () => setTimeout(diff, 150))
  } catch {
    setInterval(diff, 2_000)
  }
}

// ── Claim endpoint ─────────────────────────────────────────────────────────────
// POST /api/lavish/claim { key, agentId } — agent declares ownership of a session

import { CORS } from "./sse"

export function handleLavishClaim(req: Request, pathname: string): Response | null {
  if (req.method !== "POST" || pathname !== "/api/lavish/claim") return null
  return new Response(new ReadableStream({
    async start(controller) {
      const enc = new TextEncoder()
      try {
        const body    = await req.json()
        const key     = String(body.key     ?? "").trim()
        const agentId = String(body.agentId ?? "").trim()
        if (!key || !agentId) {
          controller.enqueue(enc.encode(JSON.stringify({ error: "key and agentId required" })))
          controller.close(); return
        }
        sessionOwners.set(key, agentId)
        controller.enqueue(enc.encode(JSON.stringify({ ok: true, key, agentId })))
      } catch { controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
      controller.close()
    },
  }), { headers: { "Content-Type": "application/json", ...CORS } })
}

// ── Proxy ──────────────────────────────────────────────────────────────────────

const INJECT = `
<style>aside.panel{display:none!important}.layout{grid-template-columns:1fr!important}</style>
<script>
;(function(){
  var _f=window.fetch
  window.fetch=function(u,i){
    if(typeof u==='string'&&u.startsWith('/')&&!u.startsWith('/lavish-proxy')&&!u.startsWith('/api/chat')&&!u.startsWith('/annotate'))
      u='/lavish-proxy'+u
    return _f.call(this,u,i)
  }
  var _o=XMLHttpRequest.prototype.open
  XMLHttpRequest.prototype.open=function(m,u){
    if(typeof u==='string'&&u.startsWith('/')&&!u.startsWith('/lavish-proxy')&&!u.startsWith('/api/chat'))
      u='/lavish-proxy'+u
    return _o.apply(this,arguments)
  }
})()
</script>`

// The pulse-agent chat panel — injected ONLY into the top-level session page.
// The artifact/whiteboard pages load in nested <iframe>s (they are proxied too,
// so they get INJECT's fetch/XHR shim), but must NOT get the panel or you end up
// with two Parlay panels nested inside each other.
const PANEL = `\n<script src="/annotate/pulse-agent.js"></script>`

export async function handleLavishProxy(req: Request, pathname: string): Promise<Response | null> {
  if (!pathname.startsWith("/lavish-proxy")) return null
  const tail = pathname.slice("/lavish-proxy".length) || "/"
  const url = new URL(req.url)
  const target = `http://127.0.0.1:4387${tail}${url.search}`

  let body: BodyInit | undefined
  if (req.method !== "GET" && req.method !== "HEAD") body = await req.arrayBuffer()

  const upstream = await fetch(target, {
    method: req.method,
    headers: { ...Object.fromEntries(req.headers), host: "127.0.0.1:4387" },
    body,
  })

  const ct = upstream.headers.get("content-type") ?? ""
  if (!ct.includes("text/html")) {
    const h = new Headers(upstream.headers)
    h.delete("content-security-policy")
    return new Response(upstream.body, { status: upstream.status, headers: h })
  }

  let html = await upstream.text()
  // Rewrite absolute asset paths through our proxy
  html = html.replace(/(href|src)="\/(?!lavish-proxy|api\/chat|annotate)/g, '$1="/lavish-proxy/')
  // Inject session owner so pulse-agent.js can route messages to the right agent
  const sessionKey = tail.replace(/^\/session\//, "").split("/")[0]
  const owner = sessionOwners.get(sessionKey)
  // Inject session owner (for message routing) and file name (for project filter)
  const sessionState = await readState()
  const lavishFile = sessionState?.[sessionKey]?.file ?? ""
  const channelScript = `<script>${owner ? `window.__paLavishChannel=${JSON.stringify(owner)};` : ""}window.__paLavishFile=${JSON.stringify(lavishFile)};</script>`
  // Only the top-level session page gets the chat panel; nested iframe pages
  // (artifact/whiteboard) get the proxy shim but NOT the panel (avoids double-nest).
  const isSessionPage = tail.startsWith("/session/")
  const inject = isSessionPage ? channelScript + INJECT + PANEL : INJECT
  html = html.replace("</body>", inject + "\n</body>")
  return new Response(html, {
    status: upstream.status,
    // no-store so the browser can't serve a stale (previously panel-injected)
    // copy of a proxied page — esp. the artifact iframe, which would otherwise
    // keep rendering the old double-nested panel until a hard refresh.
    headers: { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" },
  })
}
