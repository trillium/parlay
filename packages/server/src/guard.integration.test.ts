import { describe, expect, test, beforeAll, afterAll } from "bun:test"
import { mkdtempSync, rmSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"

// End-to-end proof against a REAL server process, on a scratch port with HOME,
// PARLAY_DATA_DIR and PAI_DIR redirected into a temp dir — it never touches
// ~/exchange, ~/.parlay, or the captain's live instance on :31337.

let proc: ReturnType<typeof Bun.spawn> | undefined
let dir = ""
let base = ""

const PORT = 45000 + Math.floor(Math.random() * 900)
const ORIGIN = `http://localhost:${PORT}`
const EVIL = "https://evil.example.com"

beforeAll(async () => {
  dir = mkdtempSync(join(tmpdir(), "parlay-guard-"))
  base = `http://127.0.0.1:${PORT}`
  proc = Bun.spawn(["bun", join(import.meta.dir, "index.ts")], {
    env: {
      ...process.env,
      HOME: dir,
      PARLAY_PORT: String(PORT),
      PARLAY_DATA_DIR: join(dir, "exchange"),
      PAI_DIR: join(dir, "pai"),
      PARLAY_STATE_HOME: join(dir, "state"),
      PARLAY_ALLOWED_ORIGINS: "",
    },
    stdout: "pipe",
    stderr: "pipe",
  })
  // Wait for the port to answer.
  for (let i = 0; i < 100; i++) {
    try {
      await fetch(`${base}/api/chat/history`)
      return
    } catch { await Bun.sleep(100) }
  }
  throw new Error("scratch parlay server never came up")
}, 20_000)

afterAll(() => {
  proc?.kill()
  if (dir) rmSync(dir, { recursive: true, force: true })
})

const send = (init: RequestInit) =>
  fetch(`${base}/api/chat/send`, { method: "POST", body: JSON.stringify({ text: "hi" }), ...init })

describe("live server: mutating routes", () => {
  test("cross-origin JSON POST /api/chat/send → 403, no ACAO", async () => {
    const r = await send({ headers: { "Content-Type": "application/json", Origin: EVIL } })
    expect(r.status).toBe(403)
    expect(r.headers.get("access-control-allow-origin")).toBeNull()
  })

  test("non-JSON Content-Type → 415", async () => {
    const r = await send({ headers: { "Content-Type": "text/plain", Origin: ORIGIN } })
    expect(r.status).toBe(415)
  })

  test("cross-origin preflight → 403 instead of a blanket 204", async () => {
    const r = await fetch(`${base}/api/chat/send`, {
      method: "OPTIONS",
      headers: { Origin: EVIL, "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type" },
    })
    expect(r.status).toBe(403)
    expect(r.headers.get("access-control-allow-origin")).toBeNull()
  })

  test("same-origin JSON POST is accepted and echoes its own origin, not '*'", async () => {
    const r = await send({ headers: { "Content-Type": "application/json", Origin: ORIGIN } })
    expect(r.status).toBe(200)
    expect(await r.json()).toMatchObject({ ok: true })
    expect(r.headers.get("access-control-allow-origin")).toBe(ORIGIN)
  })

  test("CLI-shaped POST (no Origin header) is accepted", async () => {
    const r = await send({ headers: { "Content-Type": "application/json" } })
    expect(r.status).toBe(200)
    expect(await r.json()).toMatchObject({ ok: true })
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

describe("live server: read + SSE routes keep working", () => {
  test("GET /api/chat/history still answers cross-origin", async () => {
    const r = await fetch(`${base}/api/chat/history`, { headers: { Origin: EVIL } })
    expect(r.status).toBe(200)
    expect(Array.isArray(await r.json())).toBe(true)
  })

  test("the SSE event stream still opens", async () => {
    const ac = new AbortController()
    const r = await fetch(`${base}/api/chat/events`, { signal: ac.signal })
    expect(r.status).toBe(200)
    expect(r.headers.get("content-type")).toContain("text/event-stream")
    ac.abort()
  })
})
