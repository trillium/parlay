import { CORS, broadcastToClients, broadcastToDevice, sseClients } from "./sse"

// Agent-triggerable device commands: POST /api/chat/device-cmd broadcasts a
// device_cmd SSE event that the client handles for live debugging without a
// page refresh. Commands: reload, reset-tts, ping (client echoes debug snapshot).
// If device is omitted, broadcasts to all connected clients.
export function handleDeviceCmdRequest(req: Request, pathname: string): Response | Promise<Response> | null {
  if (req.method !== "POST" || pathname !== "/api/chat/device-cmd") return null
  return new Response(new ReadableStream({
    async start(controller) {
      const enc = new TextEncoder()
      try {
        const body = await req.json()
        const cmd = String(body.cmd ?? "").trim()
        if (!cmd) {
          controller.enqueue(enc.encode(JSON.stringify({ error: "cmd required" })))
          controller.close()
          return
        }
        const device = body.device ? String(body.device).trim() : undefined
        const args = typeof body.args === "object" && body.args !== null ? body.args : {}
        const payload = { cmd, args }
        const sent = device
          ? broadcastToDevice(device, "device_cmd", payload)
          : (broadcastToClients("device_cmd", payload), sseClients.size)
        controller.enqueue(enc.encode(JSON.stringify({ ok: true, cmd, sent })))
      } catch {
        controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" })))
      }
      controller.close()
    },
  }), { headers: { "Content-Type": "application/json", ...CORS } })
}
