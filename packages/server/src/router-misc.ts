import { CORS } from "./sse"
import { broadcastAlert } from "./messages"
import { declareChannel } from "./session-channel"

// POST /api/chat/declare-channel — explicit JSON channel declaration.
// POST /api/chat/alert           — broadcast a text alert to one or all agent tabs.
// Extracted from router-messages.ts to keep that file under the 250-line limit.

export function handleMiscRequest(req: Request, pathname: string): Response | Promise<Response> | null {

  // Agents declare their session→channel mapping here instead of relying on
  // PARLAY_AGENT_ID env var + parlay-monitor arm. Written to the primary JSON
  // file ~/exchange/parlay-agent-channels.json; env/tool-activity is fallback.
  if (req.method === "POST" && pathname === "/api/chat/declare-channel") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body      = await req.json()
          const sessionId = String(body.session_id ?? "").trim()
          const channel   = String(body.channel    ?? "").trim()
          if (!sessionId || !channel) {
            controller.enqueue(enc.encode(JSON.stringify({ error: "session_id and channel required" })))
            controller.close(); return
          }
          declareChannel(sessionId, channel)
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, session_id: sessionId, channel })))
        } catch { controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  if (req.method === "POST" && pathname === "/api/chat/alert") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body = await req.json()
          const text = String(body.text ?? "").trim()
          if (!text) { controller.enqueue(enc.encode(JSON.stringify({ error: "text required" }))); controller.close(); return }
          const agentIds: string[] | undefined = Array.isArray(body.agents) && body.agents.length > 0
            ? (body.agents as unknown[]).map(String)
            : undefined
          const result = broadcastAlert(text, agentIds)
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, ...result })))
        } catch { controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  return null
}
