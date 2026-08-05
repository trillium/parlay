// /api/debug/* — lightweight server-side telemetry for client performance signals.
// In-memory only (intentionally ephemeral — restarts clear the window).
// Designed for input timing signals: autoResize cost, inter-keystroke cadence.

import { CORS } from "./sse"

type Sample = { costMs: number; sinceLastMs: number; ts: number }
type DeviceEntry = { ua: string; samples: Sample[] }

// Per-device ring buffer. Capped at 200 samples per device; entries older than
// 10 minutes are pruned on each ingest so stale sessions don't accumulate.
const devices = new Map<string, DeviceEntry>()
const MAX_SAMPLES = 200
const WINDOW_MS   = 10 * 60 * 1000

function pct(arr: number[], p: number): number {
  if (!arr.length) return 0
  const s = [...arr].sort((a, b) => a - b)
  return s[Math.max(0, Math.ceil(p * s.length) - 1)]
}

function shortUa(ua: string): string {
  if (/iPhone|iPad/i.test(ua))         return "Safari/iOS"
  if (/Android/i.test(ua))             return "Chrome/Android"
  if (/Safari/.test(ua) && !/Chrome/.test(ua)) return "Safari/macOS"
  if (/Firefox/.test(ua))              return "Firefox"
  if (/Chrome/.test(ua))               return "Chrome"
  return ua.slice(0, 24)
}

export function handleDebugRequest(req: Request, pathname: string): Response | null {
  if (!pathname.startsWith("/api/debug/input-timing")) return null

  const json = (b: unknown, s = 200) =>
    new Response(JSON.stringify(b), { status: s, headers: { "Content-Type": "application/json", ...CORS } })

  if (req.method === "OPTIONS") return new Response(null, { status: 204, headers: CORS })

  if (req.method === "POST") {
    return new Response(new ReadableStream({
      async start(ctrl) {
        const enc = new TextEncoder()
        try {
          const body = await req.json() as { device?: string; ua?: string; samples?: unknown[] }
          const device = String(body.device ?? "").trim().slice(0, 40)
          const ua     = String(body.ua     ?? "").trim().slice(0, 200)
          if (!device || !Array.isArray(body.samples) || !body.samples.length) {
            ctrl.enqueue(enc.encode(JSON.stringify({ error: "device + samples required" }))); ctrl.close(); return
          }
          const now = Date.now()
          let entry = devices.get(device)
          if (!entry) { entry = { ua, samples: [] }; devices.set(device, entry) }
          entry.ua = ua  // keep fresh
          // Prune stale
          entry.samples = entry.samples.filter(s => now - s.ts < WINDOW_MS)
          for (const raw of body.samples as any[]) {
            const s: Sample = { costMs: Number(raw.costMs ?? 0), sinceLastMs: Number(raw.sinceLastMs ?? 0), ts: now }
            if (!isFinite(s.costMs) || !isFinite(s.sinceLastMs)) continue
            entry.samples.push(s)
            if (entry.samples.length > MAX_SAMPLES) entry.samples.shift()
          }
          ctrl.enqueue(enc.encode(JSON.stringify({ ok: true, stored: entry.samples.length })))
        } catch { ctrl.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
        ctrl.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  if (req.method === "GET") {
    const now = Date.now()
    const out: Record<string, unknown> = {}
    for (const [id, entry] of devices) {
      const recent = entry.samples.filter(s => now - s.ts < WINDOW_MS)
      if (!recent.length) continue
      // Typing cost: all samples (measures layout/JS cost per keystroke)
      const costs = recent.map(s => s.costMs)
      // Cadence: filter out pauses (>1s between keys = user stopped, not UI lag)
      const cadence = recent.map(s => s.sinceLastMs).filter(v => v > 0 && v < 1000)
      out[id] = {
        ua: shortUa(entry.ua), samples: recent.length,
        cost:    { p50: pct(costs, .5), p95: pct(costs, .95), max: Math.max(...costs) },
        cadence: cadence.length ? { p50: pct(cadence, .5), p95: pct(cadence, .95) } : null,
      }
    }
    return json(out)
  }

  return null
}
