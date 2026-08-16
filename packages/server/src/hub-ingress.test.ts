import { describe, test, expect, afterAll } from "bun:test"

// hub-ingress reads PARLAY_HUB_URL once, at import — so the fixture server has
// to exist before the dynamic import below.
const received: { path: string; contentType: string | null; body: any }[] = []
let resolveNext: (() => void) | null = null

const hub = Bun.serve({
  port: 0,
  async fetch(req) {
    const url = new URL(req.url)
    received.push({
      path:        url.pathname,
      contentType: req.headers.get("content-type"),
      body:        await req.json(),
    })
    resolveNext?.()
    return Response.json({ ok: true })
  },
})
afterAll(() => hub.stop(true))

process.env.PARLAY_HUB_URL = `http://127.0.0.1:${hub.port}`
const { pushHubEvent, postHubMessage } = await import("./hub-ingress")

// Every call here is fire-and-forget, so a test has to wait for the fixture to
// actually see the request rather than awaiting the call itself.
function nextRequest(timeoutMs = 1000) {
  return new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("no request reached the hub fixture")), timeoutMs)
    resolveNext = () => { clearTimeout(timer); resolve() }
  })
}

describe("hub-ingress", () => {
  test("pushHubEvent POSTs {event, data} as JSON to /api/chat/events", async () => {
    const payload = { ts: "2026-08-15T00:00:00Z", tool: "Bash", desc: "d", cmd: "c", out: "o", err: "", channel: "c1" }
    const arrived = nextRequest()
    pushHubEvent("tool_event", payload)
    await arrived

    const got = received.at(-1)!
    expect(got.path).toBe("/api/chat/events")
    // The guard on the Go side 415s anything that is not application/json.
    expect(got.contentType).toBe("application/json")
    expect(got.body.event).toBe("tool_event")
    // Payload passthrough is the whole contract: the panel must not be able to
    // tell this frame from the in-process broadcast it replaces.
    expect(got.body.data).toEqual(payload)
  })

  test("postHubMessage sends the system_update shape hook firings depend on", async () => {
    const arrived = nextRequest()
    postHubMessage("agent", "SessionStart fired", "system", {
      type:   "system_update",
      source: "SessionStart",
      meta:   { session_id: "s-1" },
    })
    await arrived

    const got = received.at(-1)!
    expect(got.path).toBe("/api/chat/message")
    expect(got.body).toEqual({
      role:    "agent",
      text:    "SessionStart fired",
      channel: "system",
      type:    "system_update",
      source:  "SessionStart",
      meta:    { session_id: "s-1" },
    })
  })

  test("an unreachable hub neither throws nor rejects — a tailer must keep tailing", async () => {
    // A port nothing is listening on: the transport failure the in-process
    // broadcast these calls replace could never produce.
    const dead = Bun.serve({ port: 0, fetch: () => new Response("") })
    const deadPort = dead.port
    dead.stop(true)

    const rejections: unknown[] = []
    const onRejection = (e: any) => { rejections.push(e); e.preventDefault?.() }
    process.on("unhandledRejection", onRejection)
    process.env.PARLAY_HUB_URL = `http://127.0.0.1:${deadPort}`

    // Fresh module instance so it picks up the dead URL.
    const offline = await import(`./hub-ingress?dead=${deadPort}`)
    expect(() => offline.pushHubEvent("tool_event", { channel: "c1" })).not.toThrow()
    expect(() => offline.postHubMessage("agent", "x", "system", { type: "system_update" })).not.toThrow()

    await Bun.sleep(150)
    process.removeListener("unhandledRejection", onRejection)
    process.env.PARLAY_HUB_URL = `http://127.0.0.1:${hub.port}`
    expect(rejections).toEqual([])
  })
})
