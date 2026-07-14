import { randomUUID } from "crypto"
import { CORS, broadcastToClients, broadcastToDevice } from "../sse"

// ── Cursorless RPC bridge (first Parlay plugin, task-1t4) ───────────────────
// Talon-side Python POSTs an editor op here; we relay it to the panel over SSE
// (event `cursorless_rpc`), the panel's cursorless plugin applies it to the
// input box and POSTs the result back; we return it to Talon. Same waiter
// pattern as chat polling, 2.5s timeout.
//
//   POST /api/chat/plugin/cursorless/rpc      {op, args?, device?}
//   POST /api/chat/plugin/cursorless/response {rpcId, result}

interface RpcWaiter { resolve: (result: unknown) => void; timer: ReturnType<typeof setTimeout> }
const waiters = new Map<string, RpcWaiter>()

export function handlePluginRequest(req: Request, pathname: string): Response | null {
  if (req.method === "POST" && pathname === "/api/chat/plugin/cursorless/rpc") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        const reply = (obj: unknown) => { try { controller.enqueue(enc.encode(JSON.stringify(obj))); controller.close() } catch {} }
        try {
          const body = await req.json()
          const op = String(body.op ?? "")
          if (!op) { reply({ ok: false, error: "op required" }); return }
          const rpcId = randomUUID()
          const timer = setTimeout(() => {
            waiters.delete(rpcId)
            reply({ ok: false, error: "panel did not respond (2.5s)" })
          }, 2_500)
          waiters.set(rpcId, { resolve: (result) => { clearTimeout(timer); reply({ ok: true, result }) }, timer })
          const payload = { rpcId, op, args: body.args ?? null }
          const device = body.device ? String(body.device) : undefined
          const delivered = device ? broadcastToDevice(device, "cursorless_rpc", payload) : (broadcastToClients("cursorless_rpc", payload), 1)
          if (device && delivered === 0) {
            clearTimeout(timer); waiters.delete(rpcId)
            reply({ ok: false, error: `no client for device ${device}` })
          }
        } catch { reply({ ok: false, error: "bad request" }) }
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  if (req.method === "POST" && pathname === "/api/chat/plugin/cursorless/response") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body = await req.json()
          const w = waiters.get(String(body.rpcId ?? ""))
          if (w) { waiters.delete(String(body.rpcId)); w.resolve(body.result ?? null) }
          controller.enqueue(enc.encode(JSON.stringify({ ok: !!w })))
        } catch { controller.enqueue(enc.encode(JSON.stringify({ ok: false }))) }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  return null
}

// Manifest entry consumed by GET /api/chat/plugins
export const manifest = {
  id: "cursorless",
  version: "0.1.0",
  minPanel: "3.6.0",
  description: "Cursorless voice editing on the Parlay input (desktop, via Talon)",
  defaultEnabled: true,
}
