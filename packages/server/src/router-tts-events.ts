import { CORS, broadcastToClients } from "./sse"

// TTS event stream: clients POST playback lifecycle events; server broadcasts
// them as SSE `tts_event` so agents can observe real-time audio state.
export function handleTtsEventRequest(req: Request, pathname: string): Response | Promise<Response> | null {
  if (req.method !== "POST" || pathname !== "/api/chat/tts-event") return null
  return new Response(new ReadableStream({
    async start(controller) {
      const enc = new TextEncoder()
      try {
        const body = await req.json()
        const type   = String(body.type ?? "unknown")
        const device = String(body.device ?? "unknown")
        const data   = { type, device, ...body, ts: new Date().toISOString() }
        broadcastToClients("tts_event", data)
        controller.enqueue(enc.encode(JSON.stringify({ ok: true })))
      } catch {
        controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" })))
      }
      controller.close()
    },
  }), { headers: { "Content-Type": "application/json", ...CORS } })
}
