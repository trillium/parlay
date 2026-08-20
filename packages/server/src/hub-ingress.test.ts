import { describe, test, expect, afterAll } from "bun:test"

// hub-ingress reads PARLAY_HUB_URL once, at import — so the fixture server has
// to exist before the dynamic import below.
const received: { path: string; contentType: string | null; body: any }[] = []
let resolveNext: (() => void) | null = null
// When set, the fixture records the arrival and then holds the response open —
// which is how the ordering test observes that a second post is not even sent
// until the first one has completed.
let gate: Promise<void> | null = null

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
    if (gate) await gate
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

  test("posts on one route are serialized, so a burst arrives in call order", async () => {
    // The burst this reproduces: hook-firings.jsonl rotates, hook-tailer resets
    // byteOffset to 0 and re-reads every line in one synchronous pass. The
    // in-process addMessage these calls replace persisted them strictly in file
    // order; unawaited concurrent fetches let the server assign id/ts in
    // arrival order instead.
    const before = received.length
    let release!: () => void
    gate = new Promise<void>(resolve => { release = resolve })

    for (const n of [1, 2, 3]) postHubMessage("agent", `line ${n}`, "system", { type: "system_update" })

    // While the first response is held open, no later post may have been sent.
    await Bun.sleep(150)
    expect(received.length - before).toBe(1)

    release()
    gate = null
    const deadline = Date.now() + 1000
    while (received.length - before < 3 && Date.now() < deadline) await Bun.sleep(10)

    expect(received.slice(before).map(r => r.body.text)).toEqual(["line 1", "line 2", "line 3"])
  })

  test("a synchronous burst past the cap against a responsive hub loses nothing", async () => {
    // The burst the chaining exists for: hook-firings.jsonl rotates and the
    // tailer re-reads every line in one synchronous pass. Nothing can drain
    // mid-pass — no `.then` runs until the loop yields — so depth alone says
    // the queue is 300 deep while the hub is answering in milliseconds. These
    // are posts the hub PERSISTS; refusing them at the door because of a
    // number that a healthy run produces on its own loses history outright.
    const before = received.length

    const warnings: string[] = []
    const realWarn = console.warn
    console.warn = (...args: unknown[]) => { warnings.push(args.join(" ")) }

    try {
      for (let n = 0; n < 300; n++) postHubMessage("agent", `burst ${n}`, "system", { type: "system_update" })

      const deadline = Date.now() + 20_000
      while (received.length - before < 300 && Date.now() < deadline) await Bun.sleep(10)
    } finally {
      console.warn = realWarn
    }

    const delivered = received.slice(before)
    expect(delivered.map(r => r.body.text)).toEqual([...Array(300).keys()].map(n => `burst ${n}`))
    expect(warnings).toEqual([])
  }, 30_000)

  test("a hub that accepts and never answers is shed once the head is past the timeout", async () => {
    // The other half: an unresponsive hub turns every tailer tick into a link
    // that cannot start until the one ahead of it is aborted, each holding its
    // payload string, so the chain grows at the tailer's rate forever. Two
    // priming posts keep something unanswered in flight across the timeout;
    // after that the head has genuinely been waiting longer than it may, and
    // the queue is allowed to shed.
    const before = received.length
    let release!: () => void
    gate = new Promise<void>(resolve => { release = resolve })

    const warnings: string[] = []
    const realWarn = console.warn
    console.warn = (...args: unknown[]) => { warnings.push(args.join(" ")) }

    try {
      pushHubEvent("tool_event", { prime: 0 })
      pushHubEvent("tool_event", { prime: 1 })
      await Bun.sleep(5_400)

      for (let n = 0; n < 300; n++) pushHubEvent("tool_event", { n })

      release()
      gate = null
      const deadline = Date.now() + 20_000
      while (received.length - before < 257 && Date.now() < deadline) await Bun.sleep(10)
      // Long enough after the last delivery to catch a 258th that was queued
      // anyway.
      await Bun.sleep(200)
    } finally {
      console.warn = realWarn
    }

    const delivered = received.slice(before)
    expect(delivered.length).toBe(257)
    // The survivors are the OLDEST posts, in order — shedding at the door
    // never reorders what did get through.
    expect(delivered.slice(0, 2).map(r => r.body.data.prime)).toEqual([0, 1])
    expect(delivered.slice(2).map(r => r.body.data.n)).toEqual([...Array(255).keys()])
    // 45 shed posts plus the head's own abort, all inside one rate-limit
    // window: one line, not one per shed post.
    expect(warnings.length).toBe(1)
  }, 40_000)

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
