import { describe, expect, test, beforeAll, afterAll } from "bun:test"
import { existsSync, readFileSync } from "fs"
import { join } from "path"
import { EVIL, startScratchServer, type ScratchServer } from "./scratch-server"

// End-to-end proof against a REAL server process that every cross-origin
// request the verification report landed is now refused. ./integration-
// callers.test.ts is the other half: the same routes, still working for the
// panel and the CLI.

let srv: ScratchServer
let base = ""

beforeAll(async () => {
  srv = await startScratchServer()
  base = srv.base
}, 20_000)

afterAll(() => srv?.stop())

const send = (init: RequestInit) =>
  fetch(`${base}/api/chat/send`, { method: "POST", body: JSON.stringify({ text: "hi" }), ...init })

describe("live server: mutating routes", () => {
  test("cross-origin JSON POST /api/chat/send → 403, no ACAO", async () => {
    const r = await send({ headers: { "Content-Type": "application/json", Origin: EVIL } })
    expect(r.status).toBe(403)
    expect(r.headers.get("access-control-allow-origin")).toBeNull()
  })

  test("non-JSON Content-Type from an ALLOWED origin → 415", async () => {
    const r = await send({ headers: { "Content-Type": "text/plain", Origin: srv.panelOrigin } })
    expect(r.status).toBe(415)
  })

  test("the same simple content type from a DISALLOWED origin → 403, never 415", async () => {
    const r = await send({ headers: { "Content-Type": "text/plain", Origin: EVIL } })
    expect(r.status).toBe(403)
  })

  test("cross-origin preflight → 403 instead of a blanket 204", async () => {
    const r = await fetch(`${base}/api/chat/send`, {
      method: "OPTIONS",
      headers: { Origin: EVIL, "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type" },
    })
    expect(r.status).toBe(403)
    expect(r.headers.get("access-control-allow-origin")).toBeNull()
  })

  test("cross-origin fleet-wide alert → 403", async () => {
    const r = await fetch(`${base}/api/chat/alert`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: EVIL },
      body: JSON.stringify({ text: "pwned" }),
    })
    expect(r.status).toBe(403)
  })
})

// ── task-6ai1 / D9 ──────────────────────────────────────────────────────────
// The verification report ran exactly these requests against a sandbox
// instance of this code and got 200 back from every one of them. Each `403`
// below is a line that read `200` before the fix.
describe("live server: D9 — the routes the attack chain used", () => {
  // A CORS *simple request*: text/plain, no preflight. This is the shape a
  // hostile page can send with no cooperation from the server at all.
  const simple = (path: string, method = "POST", body = "{}") =>
    fetch(`${base}${path}`, { method, headers: { "Content-Type": "text/plain", Origin: EVIL }, body })

  test("POST /api/chat/eval was 200 → now 403 (drove input_action into the panel)", async () => {
    const r = await simple("/api/chat/eval", "POST", JSON.stringify({ device: "d1", text: "x" }))
    expect(r.status).toBe(403)
    expect(r.headers.get("access-control-allow-origin")).toBeNull()
  })

  test("PUT /api/chat/draft was 200 → now 403 (set the captain's outgoing text)", async () => {
    const r = await simple("/api/chat/draft", "PUT", JSON.stringify({ text: "pwned" }))
    expect(r.status).toBe(403)
    // …and the draft was not written: read it back as a legitimate caller.
    const back = await (await fetch(`${base}/api/chat/draft`)).json()
    expect(back.text).not.toBe("pwned")
  })

  test("POST /api/chat/upload was 200 → now 403", async () => {
    const form = new FormData()
    form.set("file", new File([new Uint8Array([1, 2, 3])], "x.png", { type: "image/png" }))
    const r = await fetch(`${base}/api/chat/upload`, { method: "POST", body: form, headers: { Origin: EVIL } })
    expect(r.status).toBe(403)
  })

  test("GET /api/chat/subscribers leaked device + agent ids → now 403 with no ACAO", async () => {
    const r = await fetch(`${base}/api/chat/subscribers`, { headers: { Origin: EVIL } })
    expect(r.status).toBe(403)
    expect(r.headers.get("access-control-allow-origin")).toBeNull()
    expect(await r.json()).toEqual({ error: "cross-origin request rejected" })
  })

  test("the mutating routes found alongside D9 are refused the same way", async () => {
    for (const [path, method] of [
      ["/api/chat/parlay/settings", "PUT"],
      ["/api/chat/tts", "POST"],
      ["/api/chat/tts-event", "POST"],
      ["/api/chat/plugin/cursorless/rpc", "POST"],
      ["/api/debug/input-timing", "POST"],
    ] as const) {
      const r = await simple(path, method)
      expect({ path, status: r.status }).toEqual({ path, status: 403 })
    }
  })

  test("the /rpc JSON exemption does not open it cross-origin", async () => {
    // /rpc is exempt from the content-type gate only. It is still in the
    // guarded set, so the origin check refuses a foreign page under exactly
    // the simple content types the exemption now lets a no-Origin caller use.
    for (const ct of ["text/plain", "application/x-www-form-urlencoded"]) {
      const r = await fetch(`${base}/api/chat/plugin/cursorless/rpc`, {
        method: "POST",
        headers: { "Content-Type": ct, Origin: EVIL },
        body: JSON.stringify({ op: "getEditorState" }),
      })
      expect({ ct, status: r.status, acao: r.headers.get("access-control-allow-origin") })
        .toEqual({ ct, status: 403, acao: null })
    }
  })

  test("GET /api/debug/input-timing no longer hands device ids to a foreign origin", async () => {
    const r = await fetch(`${base}/api/debug/input-timing`, { headers: { Origin: EVIL } })
    expect(r.status).toBe(403)
    expect(r.headers.get("access-control-allow-origin")).toBeNull()
  })
})

// ── GET /api/chat/poll ──────────────────────────────────────────────────────
// A GET, and a CORS *simple request* — no preflight, no content type the
// browser would refuse to send. Against the pre-fix route set every assertion
// below fails: the poll answered 200, created the agent, broadcast
// agent_register to the panel and wrote parlay-agents.json.
describe("live server: a cross-origin poll cannot register an agent", () => {
  const CHANNEL = "evil-poller-x9"

  // Collects SSE frames for `ms`, then aborts. The panel's own event stream:
  // whatever a foreign origin makes the server broadcast shows up here.
  async function sseFor(ms: number, during: () => Promise<void>): Promise<string> {
    const ac = new AbortController()
    const r = await fetch(`${base}/api/chat/events`, { signal: ac.signal })
    const reader = r.body!.getReader()
    const dec = new TextDecoder()
    let seen = ""
    const pump = (async () => {
      try {
        for (;;) {
          const { done, value } = await reader.read()
          if (done) return
          seen += dec.decode(value, { stream: true })
        }
      } catch { /* aborted */ }
    })()
    await during()
    await Bun.sleep(ms)
    ac.abort()
    await pump
    return seen
  }

  test("cross-origin GET /api/chat/poll → 403, and the registry is untouched", async () => {
    let status = 0
    let acao: string | null = "unset"
    const frames = await sseFor(300, async () => {
      const r = await fetch(`${base}/api/chat/poll?channel=${CHANNEL}`, { headers: { Origin: EVIL } })
      status = r.status
      acao = r.headers.get("access-control-allow-origin")
      expect(await r.json()).toEqual({ error: "cross-origin request rejected" })
    })

    expect(status).toBe(403)
    expect(acao).toBeNull()

    // Nothing broadcast to the panel.
    expect(frames).not.toContain(CHANNEL)
    // Nothing in the live registry, read back as a legitimate caller.
    const agents = await (await fetch(`${base}/api/chat/agents`)).json()
    expect(JSON.stringify(agents)).not.toContain(CHANNEL)
    // Nothing persisted to disk.
    const file = join(srv.dataDir, "parlay-agents.json")
    if (existsSync(file)) expect(readFileSync(file, "utf8")).not.toContain(CHANNEL)
  })
})
