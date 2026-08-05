import { randomUUID } from "crypto"
import { CORS, broadcastToClients, pollWaiters } from "./sse"

// TTS event stream: clients POST playback lifecycle events; server broadcasts
// them as SSE `tts_event` AND delivers to all poll-waiting agents so they
// receive TTS lifecycle state without a separate SSE subscription.
export function handleTtsEventRequest(req: Request, pathname: string): Response | Promise<Response> | null {
  if (req.method !== "POST" || pathname !== "/api/chat/tts-event") return null
  return new Response(new ReadableStream({
    async start(controller) {
      const enc = new TextEncoder()
      try {
        const body = await req.json()
        const type   = String(body.type ?? "unknown")
        const device = String(body.device ?? "unknown")
        const msg    = { id: randomUUID(), role: "tts_event" as const, type, device, ...body, ts: new Date().toISOString() }
        broadcastToClients("tts_event", msg)
        // Deliver to all poll-waiting agents — TTS is device-level, not channel-scoped.
        for (const w of [...pollWaiters]) w.resolve(msg)
        controller.enqueue(enc.encode(JSON.stringify({ ok: true })))
      } catch {
        controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" })))
      }
      controller.close()
    },
  }), { headers: { "Content-Type": "application/json", ...CORS } })
}
