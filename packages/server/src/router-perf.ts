import { CORS } from "./sse"
import { mkdirSync, appendFileSync } from "fs"
import { join } from "path"
import { homedir } from "os"

const PERF_DIR = join(homedir(), "data", "parlay-perf", "sessions")

mkdirSync(PERF_DIR, { recursive: true })

export function handlePerfRequest(req: Request, pathname: string): Response | null {
  if (!pathname.startsWith("/api/perf")) return null

  if (req.method === "POST" && pathname === "/api/perf/session") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body = await req.json()
          const { sessionId, startedAt, samples, summary } = body

          // Write to persistent storage: ~/data/parlay-perf/sessions/{sessionId}.jsonl
          const filename = join(PERF_DIR, `${sessionId}.jsonl`)
          const lines = [
            JSON.stringify({ type: "session_start", sessionId, startedAt }),
            ...samples.map((s: any) => JSON.stringify({ type: "sample", ...s })),
            ...(summary ? [JSON.stringify({ type: "summary", ...summary })] : []),
          ]
          appendFileSync(filename, lines.join("\n") + "\n")

          console.log(`[perf] stored ${samples.length} samples to ${filename}`)
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, file: filename })))
        } catch (e) {
          console.warn("[perf] storage error:", e)
          controller.enqueue(enc.encode(JSON.stringify({ error: String(e) })))
        }
        controller.close()
      },
    }), { status: 200, headers: { "Content-Type": "application/json", ...CORS } })
  }

  return null
}
