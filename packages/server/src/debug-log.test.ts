import { afterAll, beforeAll, describe, expect, test } from "bun:test"
import { readFileSync } from "fs"
import { join } from "path"
import { EVIL, startScratchServer, type ScratchServer } from "./guard/scratch-server"

// POST /api/chat/debug-log, end-to-end through the REAL server process —
// proving the route is actually wired in router.ts, not just that the
// handler function works. The route is guarded (it appends
// attacker-shapeable lines to a file on disk), so the same-origin/foreign
// split is asserted here too.

let s: ScratchServer

beforeAll(async () => {
  s = await startScratchServer()
})
afterAll(() => s.stop())

function post(origin: string | null, body: string, contentType = "application/json") {
  return fetch(`${s.base}/api/chat/debug-log`, {
    method: "POST",
    headers: {
      "Content-Type": contentType,
      ...(origin ? { Origin: origin } : {}),
    },
    body,
  })
}

describe("POST /api/chat/debug-log", () => {
  test("a same-origin batch lands in $PARLAY_STATE_HOME/debug.log", async () => {
    const res = await post(s.panelOrigin, JSON.stringify({
      device: "dev-test",
      ua: "test-agent",
      url: "http://panel/test",
      entries: [
        { ts: "2026-08-31T00:00:00.000Z", level: "error", source: "console.error", message: "boom", detail: { a: 1 } },
        { ts: "2026-08-31T00:00:01.000Z", level: "trace", source: "pa-jump.click", message: "scrollBottom" },
      ],
    }))
    expect(res.status).toBe(204)

    const log = readFileSync(join(s.stateDir, "debug.log"), "utf8")
    expect(log).toContain("[ERROR] device=dev-test")
    expect(log).toContain("console.error — boom")
    expect(log).toContain('{"a":1}')
    expect(log).toContain("[TRACE] device=dev-test")
    expect(log).toContain("pa-jump.click — scrollBottom")
  })

  test("an empty batch is a 204 no-op", async () => {
    const res = await post(s.panelOrigin, JSON.stringify({ device: "d", entries: [] }))
    expect(res.status).toBe(204)
  })

  test("invalid JSON is a 400", async () => {
    const res = await post(s.panelOrigin, "not json {")
    expect(res.status).toBe(400)
  })

  test("a foreign origin is refused before the handler runs", async () => {
    const res = await post(EVIL, JSON.stringify({
      device: "evil", entries: [{ level: "error", source: "x", message: "must-not-land" }],
    }))
    expect(res.status).toBe(403)
    const log = readFileSync(join(s.stateDir, "debug.log"), "utf8")
    expect(log).not.toContain("must-not-land")
  })

  test("a no-Origin client (curl/CLI shape) is allowed", async () => {
    const res = await post(null, JSON.stringify({
      device: "cli", entries: [{ level: "warn", source: "curl", message: "no-origin ok" }],
    }))
    expect(res.status).toBe(204)
  })
})
