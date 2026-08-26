// The --agent-reply POST, and the deadline's authority over the grace window.
//
// Both cases here are the same defect shape as the four this file's sibling
// tests cover: an operation that cannot report the difference between having
// worked and having been ignored.

import { test, expect, describe } from "bun:test"
import { runBridge, DEAD } from "./harness"

describe("--agent-reply reports delivery honestly", () => {
  test("a 500 from /api/chat/reply is reported on stderr", async () => {
    // fetch resolves on 4xx/5xx — it only rejects on a transport failure. So a
    // server that refuses the reply used to be indistinguishable from one that
    // accepted it, and the agent's answer vanished silently.
    const srv = Bun.serve({
      port: 0,
      fetch(req) {
        if (new URL(req.url).pathname === "/api/chat/reply") {
          return new Response("nope", { status: 500 })
        }
        return new Promise<Response>(() => {}) // stall the poll until the deadline
      },
    })
    try {
      const r = await runBridge({
        args: ["doc.md", "--agent-reply", "here is the fix", "--timeout-ms", "400"],
        parlayUrl: `http://127.0.0.1:${srv.port}`,
      })
      expect(r.stderr).toContain("reply post rejected")
      expect(r.stderr).toContain("500")
      // The reply failing must not take the bridge down — polling still ran and
      // still produced its normal terminal record.
      expect(r.code).toBe(0)
      expect(r.json?.session?.status).toBe("waiting")
    } finally {
      srv.stop(true)
    }
  })

  test("a 404 is reported too, not just 5xx", async () => {
    const srv = Bun.serve({
      port: 0,
      fetch: (req) =>
        new URL(req.url).pathname === "/api/chat/reply"
          ? new Response("", { status: 404 })
          : new Promise<Response>(() => {}),
    })
    try {
      const r = await runBridge({
        args: ["doc.md", "--agent-reply", "hello", "--timeout-ms", "400"],
        parlayUrl: `http://127.0.0.1:${srv.port}`,
      })
      expect(r.stderr).toContain("reply post rejected")
      expect(r.stderr).toContain("404")
    } finally {
      srv.stop(true)
    }
  })

  test("a 200 reply posts the expected body and says nothing on stderr", async () => {
    let body: any = null
    const srv = Bun.serve({
      port: 0,
      async fetch(req) {
        const u = new URL(req.url)
        if (u.pathname === "/api/chat/reply") {
          body = await req.json()
          return Response.json({ ok: true })
        }
        return new Promise<Response>(() => {}) // stall the poll
      },
    })
    try {
      const r = await runBridge({
        args: ["doc.md", "--agent-reply", "all done", "--timeout-ms", "400"],
        parlayUrl: `http://127.0.0.1:${srv.port}`,
      })
      expect(body?.text).toBe("all done")
      expect(body?.agent).toBe("agent-test")
      expect(r.stderr).not.toContain("reply post")
    } finally {
      srv.stop(true)
    }
  })

  test("an unreachable Parlay reports a transport failure and still polls", async () => {
    const r = await runBridge({
      args: ["doc.md", "--agent-reply", "into the void", "--timeout-ms", "400"],
      parlayUrl: DEAD,
    })
    expect(r.stderr).toContain("reply post failed")
    expect(r.code).toBe(0)
  })
})

describe("the grace window cannot outlive the deadline", () => {
  test("a native snapshot arriving after the deadline is not waited for", async () => {
    // Asserted behaviourally rather than by wall-clock: a 200ms overshoot is
    // smaller than `bun` process-startup jitter, so a timing bound loose enough
    // not to flake is also too loose to fail — a test that cannot fail is not a
    // test. Instead the two implementations differ in OUTPUT here.
    //
    // Parlay answers instantly, so the grace window opens at ~t0. The native
    // side answers at t0+150ms, and the deadline is t0+100ms.
    //   capped   -> graceMs is the ~90ms left, native misses it, dom_snapshot ""
    //   uncapped -> a flat 200ms wait, native lands at 150ms, snapshot present
    const parlaySrv = Bun.serve({
      port: 0,
      fetch: () => Response.json({ id: "g1", role: "user", text: "urgent" }),
    })
    const nativeSrv = Bun.serve({
      port: 0,
      async fetch() {
        await Bun.sleep(150)
        return Response.json({ status: "waiting", dom_snapshot: "<after-the-deadline/>" })
      },
    })
    try {
      const r = await runBridge({
        args: ["doc.md", "--timeout-ms", "100"],
        parlayUrl: `http://127.0.0.1:${parlaySrv.port}`,
        nativeUrl: `http://127.0.0.1:${nativeSrv.port}`,
      })
      // The message still gets through — capping the grace must not cost data.
      expect(r.code).toBe(0)
      expect(r.json?.prompts?.[0]?.text).toBe("urgent")
      expect(r.json?.dom_snapshot).toBe("")
    } finally {
      parlaySrv.stop(true)
      nativeSrv.stop(true)
    }
  })

  test("with no --timeout-ms the grace window is still honoured in full", async () => {
    // deadline is Infinity here, so the cap must reduce to NATIVE_GRACE_MS and
    // not to zero — otherwise this fix would silently delete the grace window
    // that the shared-AbortController fix exists to make usable.
    const parlaySrv = Bun.serve({
      port: 0,
      fetch: () => Response.json({ id: "g2", role: "user", text: "no deadline" }),
    })
    const nativeSrv = Bun.serve({
      port: 0,
      async fetch() {
        await Bun.sleep(60)
        return Response.json({ status: "waiting", dom_snapshot: "<kept/>" })
      },
    })
    try {
      const r = await runBridge({
        args: ["doc.md"],
        parlayUrl: `http://127.0.0.1:${parlaySrv.port}`,
        nativeUrl: `http://127.0.0.1:${nativeSrv.port}`,
      })
      expect(r.json?.prompts?.[0]?.text).toBe("no deadline")
      expect(r.json?.dom_snapshot).toBe("<kept/>")
    } finally {
      parlaySrv.stop(true)
      nativeSrv.stop(true)
    }
  })
})
