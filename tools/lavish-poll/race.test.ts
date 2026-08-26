// The Parlay-vs-4387 race: the grace window that lets a chat message still
// carry a dom_snapshot, and the deadline that has to be a leg of the race
// rather than only the loop condition.

import { test, expect, describe } from "bun:test"
import { runBridge, stalling, DEAD } from "./harness"

describe("the deadline is enforced, not merely declared", () => {
  test("--timeout-ms bounds a Parlay long-poll that never answers", async () => {
    // The deadline used to be checked only as the `while` loop condition, so a
    // request still in flight blocked past it. With 4387 unreachable its leg of
    // the race never settles either (see drop()), leaving nothing to break the
    // await — this exact setup hung indefinitely regardless of --timeout-ms.
    const parlaySrv = stalling()
    try {
      const started = Date.now()
      const r = await runBridge({
        args: ["doc.md", "--timeout-ms", "800"],
        parlayUrl: `http://127.0.0.1:${parlaySrv.port}`,
      })
      expect(r.code).toBe(0)
      expect(r.json?.session?.status).toBe("waiting")
      // Generous upper bound — the point is that it terminates at all, and
      // anywhere near 800ms rather than never.
      expect(Date.now() - started).toBeLessThan(6_000)
    } finally {
      parlaySrv.stop(true)
    }
  })

  test("--timeout-ms bounds a run where BOTH upstreams stall", async () => {
    const parlaySrv = stalling()
    const nativeSrv = stalling()
    try {
      const r = await runBridge({
        args: ["doc.md", "--timeout-ms", "800"],
        parlayUrl: `http://127.0.0.1:${parlaySrv.port}`,
        nativeUrl: `http://127.0.0.1:${nativeSrv.port}`,
      })
      expect(r.code).toBe(0)
      expect(r.json?.session?.status).toBe("waiting")
    } finally {
      parlaySrv.stop(true)
      nativeSrv.stop(true)
    }
  })

  test("a valid --timeout-ms expires when both upstreams are unreachable", async () => {
    const r = await runBridge({ args: ["doc.md", "--timeout-ms", "700"] })
    expect(r.code).toBe(0)
    expect(r.json?.session?.status).toBe("waiting")
  })
})

describe("native grace window", () => {
  test("a chat message carries the dom_snapshot the native side supplies", async () => {
    // Pre-fix this was impossible: one shared AbortController meant ac.abort()
    // killed the native request the instant Parlay won, so the native promise
    // had already settled to null via its own .catch() before the grace window
    // read it. dom_snapshot was unconditionally "".
    const parlaySrv = Bun.serve({
      port: 0,
      fetch: () => Response.json({ id: "c1", role: "user", text: "please fix the header" }),
    })
    const nativeSrv = Bun.serve({
      port: 0,
      async fetch() {
        await Bun.sleep(60) // lose the race, but land inside the grace window
        return Response.json({
          status: "waiting",
          dom_snapshot: "<html>snapshot</html>",
          layout_warnings: ["overflow on .header"],
        })
      },
    })
    try {
      const r = await runBridge({
        args: ["doc.md"],
        parlayUrl: `http://127.0.0.1:${parlaySrv.port}`,
        nativeUrl: `http://127.0.0.1:${nativeSrv.port}`,
      })
      expect(r.code).toBe(0)
      expect(r.json?.session?.status).toBe("feedback")
      expect(r.json?.prompts?.[0]?.text).toBe("please fix the header")
      expect(r.json?.dom_snapshot).toBe("<html>snapshot</html>")
      expect(r.json?.layout_warnings).toEqual(["overflow on .header"])
    } finally {
      parlaySrv.stop(true)
      nativeSrv.stop(true)
    }
  })

  test("a native side slower than the grace window still yields the chat message", async () => {
    const parlaySrv = Bun.serve({
      port: 0,
      fetch: () => Response.json({ id: "c2", role: "user", text: "hello" }),
    })
    const nativeSrv = Bun.serve({
      port: 0,
      async fetch() {
        await Bun.sleep(5_000) // well past NATIVE_GRACE_MS
        return Response.json({ status: "waiting", dom_snapshot: "too late" })
      },
    })
    try {
      const started = Date.now()
      const r = await runBridge({
        args: ["doc.md"],
        parlayUrl: `http://127.0.0.1:${parlaySrv.port}`,
        nativeUrl: `http://127.0.0.1:${nativeSrv.port}`,
      })
      expect(r.code).toBe(0)
      expect(r.json?.prompts?.[0]?.text).toBe("hello")
      expect(r.json?.dom_snapshot).toBe("")
      // The grace window is a cap, not a wait-for-completion.
      expect(Date.now() - started).toBeLessThan(4_000)
    } finally {
      parlaySrv.stop(true)
      nativeSrv.stop(true)
    }
  })

  test("an unreachable native side does not stop a chat message being delivered", async () => {
    const parlaySrv = Bun.serve({
      port: 0,
      fetch: () => Response.json({ id: "c3", role: "user", text: "still works" }),
    })
    try {
      const r = await runBridge({
        args: ["doc.md"],
        parlayUrl: `http://127.0.0.1:${parlaySrv.port}`,
        nativeUrl: DEAD,
      })
      expect(r.code).toBe(0)
      expect(r.json?.prompts?.[0]?.text).toBe("still works")
      expect(r.json?.dom_snapshot).toBe("")
    } finally {
      parlaySrv.stop(true)
    }
  })

  test("the native side winning the race still ends the session correctly", async () => {
    const parlaySrv = stalling()
    const nativeSrv = Bun.serve({
      port: 0,
      fetch: () => Response.json({ status: "ended", ended_by: "user", dom_snapshot: "<final/>" }),
    })
    try {
      const r = await runBridge({
        args: ["doc.md"],
        parlayUrl: `http://127.0.0.1:${parlaySrv.port}`,
        nativeUrl: `http://127.0.0.1:${nativeSrv.port}`,
      })
      expect(r.code).toBe(0)
      expect(r.json?.session?.status).toBe("ended")
      expect(r.json?.session?.ended_by).toBe("user")
      expect(r.json?.dom_snapshot).toBe("<final/>")
    } finally {
      parlaySrv.stop(true)
      nativeSrv.stop(true)
    }
  })

  test("native layout warnings are delivered when 4387 wins the race", async () => {
    const parlaySrv = stalling()
    const nativeSrv = Bun.serve({
      port: 0,
      fetch: () =>
        Response.json({ status: "waiting", layout_warnings: ["clipped .sidebar"], prompts: [] }),
    })
    try {
      const r = await runBridge({
        args: ["doc.md"],
        parlayUrl: `http://127.0.0.1:${parlaySrv.port}`,
        nativeUrl: `http://127.0.0.1:${nativeSrv.port}`,
      })
      expect(r.code).toBe(0)
      expect(r.json?.session?.status).toBe("feedback")
      expect(r.json?.layout_warnings).toEqual(["clipped .sidebar"])
    } finally {
      parlaySrv.stop(true)
      nativeSrv.stop(true)
    }
  })
})
