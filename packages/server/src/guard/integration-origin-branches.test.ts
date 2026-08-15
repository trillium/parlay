import { describe, expect, test, beforeAll, afterAll } from "bun:test"
import { startScratchServer, type ScratchServer } from "./scratch-server"

// originAllowed accepts on two independent branches, and the panel's own
// origin (http://127.0.0.1:<port>) satisfies both at once — so no case in
// ./integration-callers.test.ts proves either one on its own. Each branch gets
// isolated here, against a REAL server process, under a name that says which
// it is. Host and Origin are just headers; no DNS is involved and nothing is
// dialled but `base`.

let srv: ScratchServer
let base = ""

beforeAll(async () => {
  srv = await startScratchServer()
  base = srv.base
}, 20_000)

afterAll(() => srv?.stop())

describe("live server: the loopback-HOSTNAME branch (localhost ≠ base's host)", () => {
  test("the fixture's host really does differ from base's, so same-host cannot be what accepts it", () => {
    expect(new URL(srv.loopbackOrigin).host).not.toBe(new URL(base).host)
  })

  test("guarded POST is accepted and echoes that origin, not '*'", async () => {
    const r = await fetch(`${base}/api/chat/send`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: srv.loopbackOrigin },
      body: JSON.stringify({ text: "from localhost" }),
    })
    expect(r.status).toBe(200)
    expect(await r.json()).toMatchObject({ ok: true })
    expect(r.headers.get("access-control-allow-origin")).toBe(srv.loopbackOrigin)
  })

  test("guarded GET /api/chat/subscribers is accepted", async () => {
    const r = await fetch(`${base}/api/chat/subscribers`, { headers: { Origin: srv.loopbackOrigin } })
    expect(r.status).toBe(200)
    expect(r.headers.get("access-control-allow-origin")).toBe(srv.loopbackOrigin)
    expect(await r.json()).toHaveProperty("registered.count")
  })

  test("preflight on a guarded route succeeds", async () => {
    const r = await fetch(`${base}/api/chat/draft`, {
      method: "OPTIONS",
      headers: { Origin: srv.loopbackOrigin, "Access-Control-Request-Method": "PUT", "Access-Control-Request-Headers": "content-type" },
    })
    expect(r.status).toBe(204)
    expect(r.headers.get("access-control-allow-origin")).toBe(srv.loopbackOrigin)
  })
})

// The same-host comparison in ./origin.ts exists for one deployment shape: a
// panel reached through a Host-forwarding tunnel or reverse proxy under a name
// that is NOT loopback, private-LAN or .local. Every other origin this suite
// sends is a local hostname and would still be accepted with that comparison
// deleted; `tunnelOrigin` is the one that cannot be. If the branch silently
// regresses, a tunnelled panel starts getting 403 on every mutating route
// while the rest of the suite stays green — which is exactly what these cases
// exist to catch.
describe("live server: the SAME-HOST branch (a non-local name, forwarded Host)", () => {
  test("the fixture's hostname is not one isLocalHostname would accept", () => {
    const h = new URL(srv.tunnelOrigin).hostname
    expect(h).not.toBe("localhost")
    expect(h.endsWith(".local")).toBe(false)
    expect(/^(10\.|127\.|169\.254\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(h)).toBe(false)
  })

  test("guarded PUT /api/chat/draft on its own forwarded Host reaches the handler", async () => {
    const r = await fetch(`${base}/api/chat/draft`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", Origin: srv.tunnelOrigin, Host: srv.tunnelHost },
      body: JSON.stringify({ text: "tunnelled draft" }),
    })
    expect(r.status).toBe(200)
    expect(r.headers.get("access-control-allow-origin")).toBe(srv.tunnelOrigin)
    const back = await fetch(`${base}/api/chat/draft`, { headers: { Origin: srv.tunnelOrigin, Host: srv.tunnelHost } })
    expect((await back.json()).text).toBe("tunnelled draft")
  })

  test("guarded POST /api/chat/send on its own forwarded Host is accepted", async () => {
    const r = await fetch(`${base}/api/chat/send`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: srv.tunnelOrigin, Host: srv.tunnelHost },
      body: JSON.stringify({ text: "through the tunnel" }),
    })
    expect(r.status).toBe(200)
    expect(await r.json()).toMatchObject({ ok: true })
    expect(r.headers.get("access-control-allow-origin")).toBe(srv.tunnelOrigin)
  })

  test("the control: the SAME origin arriving on a different Host is refused, no ACAO", async () => {
    const r = await fetch(`${base}/api/chat/draft`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", Origin: srv.tunnelOrigin, Host: srv.tunnelForeignHost },
      body: JSON.stringify({ text: "should never land" }),
    })
    expect(r.status).toBe(403)
    expect(r.headers.get("access-control-allow-origin")).toBeNull()
    expect(await r.json()).toEqual({ error: "cross-origin request rejected" })
  })

  test("the control: preflight from that origin on a different Host is refused too", async () => {
    const r = await fetch(`${base}/api/chat/draft`, {
      method: "OPTIONS",
      headers: { Origin: srv.tunnelOrigin, Host: srv.tunnelForeignHost, "Access-Control-Request-Method": "PUT" },
    })
    expect(r.status).toBe(403)
    expect(r.headers.get("access-control-allow-origin")).toBeNull()
  })
})
