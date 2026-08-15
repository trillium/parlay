import { describe, expect, test, beforeAll, afterAll } from "bun:test"
import { EVIL, startScratchServer, uploadForm, type ScratchServer } from "./scratch-server"

// The other half of ./integration-attack.test.ts: against a REAL server
// process, every legitimate caller the guard must not break — the panel on its
// own origin, the CLI/curl with no Origin at all, and the read/SSE routes that
// stay deliberately world-readable.

let srv: ScratchServer
let base = ""
// The panel's real origin, http://127.0.0.1:<port>. It satisfies both of
// originAllowed's accept branches at once — its host:port equals the one the
// request was sent to, and 127. is also a private-v4 literal — so the cases
// using it prove the panel works, not which branch let it in. The two fixtures
// below each isolate one branch.
// ./integration-origin-branches.test.ts is where each accept branch is
// isolated end-to-end.
let ORIGIN = ""

beforeAll(async () => {
  srv = await startScratchServer()
  base = srv.base
  ORIGIN = srv.panelOrigin
}, 20_000)

afterAll(() => srv?.stop())

describe("live server: the panel and the CLI are unaffected", () => {
  test("CLI-shaped POST /api/chat/send (no Origin header) is accepted", async () => {
    const r = await fetch(`${base}/api/chat/send`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: "hi" }),
    })
    expect(r.status).toBe(200)
    expect(await r.json()).toMatchObject({ ok: true })
  })

  test("panel: same-origin POST is accepted and echoes its own origin, not '*'", async () => {
    const r = await fetch(`${base}/api/chat/send`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: ORIGIN },
      body: JSON.stringify({ text: "hi" }),
    })
    expect(r.status).toBe(200)
    expect(await r.json()).toMatchObject({ ok: true })
    expect(r.headers.get("access-control-allow-origin")).toBe(ORIGIN)
  })

  test("panel: same-origin PUT then GET /api/chat/draft round-trips", async () => {
    const put = await fetch(`${base}/api/chat/draft`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", Origin: ORIGIN },
      body: JSON.stringify({ text: "panel draft" }),
    })
    expect(put.status).toBe(200)
    expect(put.headers.get("access-control-allow-origin")).toBe(ORIGIN)
    const get = await fetch(`${base}/api/chat/draft`, { headers: { Origin: ORIGIN } })
    expect((await get.json()).text).toBe("panel draft")
  })

  test("CLI/curl: no-Origin GET /api/chat/subscribers still returns the full snapshot", async () => {
    const r = await fetch(`${base}/api/chat/subscribers`)
    expect(r.status).toBe(200)
    const body = await r.json()
    // The exact keys `parlay subscribers`, `parlay doctor` and crew-state read.
    expect(body).toHaveProperty("parlay.clients")
    expect(body).toHaveProperty("registered.count")
    expect(body).toHaveProperty("presence")
    expect(body).toHaveProperty("devices")
  })

  test("panel: same-origin multipart upload still works and serves back", async () => {
    const r = await fetch(`${base}/api/chat/upload`, { method: "POST", body: uploadForm(), headers: { Origin: ORIGIN } })
    expect(r.status).toBe(200)
    const { ok, url } = await r.json()
    expect(ok).toBe(true)
    // The serve route stays unguarded so an <img> can load it.
    const img = await fetch(`${base}${url}`, { headers: { Origin: EVIL } })
    expect(img.status).toBe(200)
    expect(img.headers.get("content-type")).toBe("image/gif")
  })

  test("CLI/curl: no-Origin multipart upload is not 415'd by the JSON gate", async () => {
    const r = await fetch(`${base}/api/chat/upload`, { method: "POST", body: uploadForm() })
    expect(r.status).toBe(200)
    expect((await r.json()).ok).toBe(true)
  })

  // Talon: an out-of-repo Python caller whose JSON body may arrive under
  // whatever content type `requests` gave it. The handler is `await
  // req.json()`, which parses the body regardless of the header, so the route's
  // contract is the body — hence the JSON_EXEMPT_PATHS entry. `device` names a
  // client that is not connected, so the handler answers immediately with a
  // string only it can produce, instead of holding the 2.5s waiter open.
  for (const ct of ["text/plain", "application/x-www-form-urlencoded"]) {
    test(`Talon-shaped no-Origin POST /rpc under ${ct} reaches the handler`, async () => {
      const r = await fetch(`${base}/api/chat/plugin/cursorless/rpc`, {
        method: "POST",
        headers: { "Content-Type": ct },
        body: JSON.stringify({ op: "getEditorState", device: "no-such-device" }),
      })
      expect(r.status).toBe(200)
      expect(await r.json()).toEqual({ ok: false, error: "no client for device no-such-device" })
    })
  }

  test("panel: same-origin POST /api/chat/eval reaches the handler", async () => {
    // PARLAY_EVAL_ENGINE_URL points at a dead port (see ./scratch-server), so
    // the handler answers 502 "engine unreachable" — which only the handler can
    // produce. The guard would have answered 403 or 415.
    const r = await fetch(`${base}/api/chat/eval`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: ORIGIN },
      body: JSON.stringify({ device: "panel-device", text: "hello", version: 1 }),
    })
    expect(r.status).toBe(502)
    expect((await r.json()).error).toBe("engine unreachable")
    expect(r.headers.get("access-control-allow-origin")).toBe(ORIGIN)
  })

  test("panel: same-origin settings PUT/GET still round-trips", async () => {
    const put = await fetch(`${base}/api/chat/parlay/settings`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", Origin: ORIGIN },
      body: JSON.stringify({ textScale: 120 }),
    })
    expect(put.status).toBe(200)
    expect((await put.json()).ok).toBe(true)
    const get = await fetch(`${base}/api/chat/parlay/settings`, { headers: { Origin: ORIGIN } })
    expect((await get.json()).textScale).toBe(120)
  })

  // The pollers this repo actually has — the relay, both CLI monitors,
  // tools/split-test, pages/chat/agent-notify.ts — are all no-Origin HTTP
  // clients. Guarding /poll must leave them untouched.
  // Queue a message first so each poll below returns immediately instead of
  // holding its connection open for 30s. The handler registers the channel
  // before it looks for pending work either way, so this still exercises the
  // write the guard now sits in front of.
  const queueFor = async (channel: string) => {
    const r = await fetch(`${base}/api/chat/send`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: `queued for ${channel}`, toAgent: channel }),
    })
    expect((await r.json()).ok).toBe(true)
  }

  test("CLI/relay: no-Origin GET /api/chat/poll reaches the handler and registers", async () => {
    await queueFor("cli-poller")
    const r = await fetch(`${base}/api/chat/poll?channel=cli-poller`)
    expect(r.status).toBe(200)
    expect((await r.json()).text).toBe("queued for cli-poller")
    const agents = await (await fetch(`${base}/api/chat/agents`)).json()
    expect(JSON.stringify(agents)).toContain("cli-poller")
  })

  test("panel-origin GET /api/chat/poll is accepted and echoes its own origin, not '*'", async () => {
    await queueFor("panel-poller")
    const r = await fetch(`${base}/api/chat/poll?channel=panel-poller`, { headers: { Origin: ORIGIN } })
    expect(r.status).toBe(200)
    expect(r.headers.get("access-control-allow-origin")).toBe(ORIGIN)
    expect((await r.json()).text).toBe("queued for panel-poller")
  })

  test("panel: same-origin preflight on a newly guarded route still succeeds", async () => {
    const r = await fetch(`${base}/api/chat/draft`, {
      method: "OPTIONS",
      headers: { Origin: ORIGIN, "Access-Control-Request-Method": "PUT", "Access-Control-Request-Headers": "content-type" },
    })
    expect(r.status).toBe(204)
    expect(r.headers.get("access-control-allow-origin")).toBe(ORIGIN)
    expect(r.headers.get("access-control-allow-methods")).toContain("PUT")
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
