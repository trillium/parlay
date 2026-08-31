import { broadcastToDevice, CORS } from "./sse"

// ── Server-side eval relay (feat/server-side-eval) ─────────────────────────────
//
// PURE server-side input evaluation. The TS server does NOT evaluate — it is a
// thin relay between the client and the COMPILED Go engine (tools/cli/internal/evalengine).
// This is deliberate: the captain's whole point is that evaluation runs as
// compiled RE2 in Go, not interpreted JS. Doing the matching here in bun would
// defeat the purpose. So this file contains ZERO command-matching logic — only
// transport.
//
//   up:   client POST /api/chat/eval  ──▶  this relay  ──▶  Go /eval (HTTP)
//   down: Go /eval response.actions    ──▶  broadcastToDevice(input_action) ─▶ SSE
//   fire: Go server-owned submit timer ──▶  POST /api/chat/eval-push ─▶ SSE
//
// Input evaluation is PURE server-side and unconditional — there is no local
// pipeline and no enable flag. Every keystroke is relayed to the Go engine.

const EVAL_ENGINE_URL =
  process.env.PARLAY_EVAL_ENGINE_URL ?? "http://127.0.0.1:4343"

// The action-protocol envelope the client dispatcher expects. Built from the Go
// engine's response; the relay is agnostic to the verbs inside.
interface EvalEnvelope {
  v: number
  streamId: string
  seq: number
  baseVersion: number
  actions: unknown[]
  engineEvalNs: number
  fired: string
}

// Relay round-trip timing surfaced to the client so it can display the
// compiled-eval-time vs. total-round-trip comparison the captain wants to see.
interface RelayTiming {
  engineEvalNs: number   // pure compiled eval time (from Go)
  relayMs: number        // TS→Go→TS HTTP round-trip as seen by the relay
}

export async function handleEvalRequest(
  req: Request,
  pathname: string,
): Promise<Response | null> {
  // POST /api/chat/eval — the up-channel. Delegates to the compiled Go engine
  // and relays its actions over the device-scoped SSE.
  if (req.method === "POST" && pathname === "/api/chat/eval") {
    let body: {
      streamId?: string
      version?: number
      text?: string
      cursor?: { anchor: number; active: number }
      reason?: string
      voiceEnabled?: boolean
      tabs?: { id: string; name: string }[]
      device?: string
    }
    try {
      body = await req.json()
    } catch {
      return json({ error: "bad request" }, 400)
    }
    const device = String(body.device ?? "").trim()
    if (!device) return json({ error: "device required" }, 400)
    const streamId = String(body.streamId ?? `eval-${device}-main`)

    const engineReq = {
      streamId,
      version: Number(body.version ?? 0),
      text: String(body.text ?? ""),
      cursor: body.cursor ?? { anchor: 0, active: 0 },
      reason: String(body.reason ?? "input"),
      voiceEnabled: body.voiceEnabled === true,
      tabs: Array.isArray(body.tabs) ? body.tabs : [],
    }

    // Remember which device owns this stream so a later server-owned submit fire
    // (which arrives on /eval-push with only a streamId) can be routed back.
    streamDevice.set(streamId, device)

    const t0 = performance.now()
    let env: EvalEnvelope
    try {
      const r = await fetch(`${EVAL_ENGINE_URL}/eval`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(engineReq),
        signal: AbortSignal.timeout(2_000),
      })
      if (!r.ok) return json({ error: `engine ${r.status}` }, 502)
      env = (await r.json()) as EvalEnvelope
    } catch (e) {
      // Engine unreachable — fail loud but non-fatal. No local fallback exists;
      // commands simply don't fire for this keystroke until the engine returns.
      return json({ error: "engine unreachable", detail: String(e) }, 502)
    }
    const relayMs = performance.now() - t0

    const timing: RelayTiming = { engineEvalNs: env.engineEvalNs, relayMs }

    // Relay the actions over the existing device-scoped SSE as one input_action
    // event carrying the full envelope. The client dispatcher validates seq /
    // baseVersion / ttl before applying (dispatcher.ts).
    const matched = broadcastToDevice(device, "input_action", {
      v: env.v,
      streamId: env.streamId,
      seq: env.seq,
      baseVersion: env.baseVersion,
      actions: env.actions,
      timing,
    })

    // Also return the envelope+timing synchronously so a curl/Interceptor probe
    // (and the client's latency overlay) can read it without the SSE hop.
    return json({ ok: true, sseClients: matched, ...env, timing }, 200)
  }

  // POST /api/chat/eval-push — the down-channel for SERVER-OWNED submit fires.
  // The Go engine calls this when its per-stream 1s timer elapses; we look up the
  // owning device and push the submitNow over SSE. This is the leg that carries
  // the network-race cost: the fire is already ~1 round-trip stale by the time it
  // reaches the client, which re-verifies the tail before actually sending.
  if (req.method === "POST" && pathname === "/api/chat/eval-push") {
    let body: { streamId?: string; seq?: number; baseVersion?: number; v?: number; action?: unknown }
    try {
      body = await req.json()
    } catch {
      return json({ error: "bad request" }, 400)
    }
    const streamId = String(body.streamId ?? "")
    const device = streamDevice.get(streamId)
    if (!device) return json({ error: "unknown stream", streamId }, 404)

    const matched = broadcastToDevice(device, "input_action", {
      v: body.v ?? 1,
      streamId,
      seq: body.seq ?? 0,
      baseVersion: body.baseVersion ?? 0,
      actions: body.action ? [body.action] : [],
      timing: { serverOwnedFire: true },
    })
    return json({ ok: true, sseClients: matched }, 200)
  }

  return null
}

// streamId → deviceId, so a server-owned submit fire (which only knows the
// stream) can be routed to the right SSE device. In-memory is fine: a Pulse
// restart drops armed timers anyway, and the client re-arms on the next eval.
const streamDevice = new Map<string, string>()

function json(data: unknown, status: number): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json", ...CORS },
  })
}
