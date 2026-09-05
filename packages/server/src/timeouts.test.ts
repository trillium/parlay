import { describe, test, expect } from "bun:test"
import { serve } from "bun"
import { readFileSync } from "fs"
import { join } from "path"
import {
  POLL_WAIT_MS, SSE_KEEPALIVE_MS, SERVER_IDLE_TIMEOUT_SEC,
  BUN_MAX_IDLE_TIMEOUT_SEC, BUN_DEFAULT_IDLE_TIMEOUT_SEC,
} from "./timeouts"

// task-2nwlw. The chat server wedged in production: TCP kept accepting on its
// port while every long-lived route answered HTTP 200 with an EMPTY body at 0%
// CPU. Nothing in parlay was blocking the event loop — `Bun.serve()` was handed
// no `idleTimeout`, so Bun's 10s default destroyed the connections this server
// intentionally holds open for longer: a 30s long-poll, and an SSE stream whose
// first keepalive is 25s away and therefore could never fire even once. The
// runtime tearing down stream controllers that JS timers still referenced is
// also the path each `panic(main thread): Segmentation fault at address 0x0`
// was logged immediately after.

const LONGEST_HOLD_MS = Math.max(POLL_WAIT_MS, SSE_KEEPALIVE_MS)

describe("SERVER_IDLE_TIMEOUT_SEC invariants", () => {
  test("outlives every connection this server intentionally holds open", () => {
    // The regression itself: once a hold grows past the timeout, that route
    // starts answering empty-bodied 200s in production.
    expect(SERVER_IDLE_TIMEOUT_SEC * 1000).toBeGreaterThan(POLL_WAIT_MS)
    expect(SERVER_IDLE_TIMEOUT_SEC * 1000).toBeGreaterThan(SSE_KEEPALIVE_MS)
  })

  test("Bun's default would NOT outlive them — which is why it must be set explicitly", () => {
    expect(BUN_DEFAULT_IDLE_TIMEOUT_SEC * 1000).toBeLessThan(LONGEST_HOLD_MS)
  })

  test("stays inside what Bun.serve accepts", () => {
    expect(Number.isInteger(SERVER_IDLE_TIMEOUT_SEC)).toBe(true)
    expect(SERVER_IDLE_TIMEOUT_SEC).toBeGreaterThan(0)
    expect(SERVER_IDLE_TIMEOUT_SEC).toBeLessThanOrEqual(BUN_MAX_IDLE_TIMEOUT_SEC)
  })

  test("the SSE keepalive can actually fire before the connection is closed", () => {
    // A keepalive scheduled after the idle timeout is dead code: the runtime
    // closes the stream first, every time, so it never runs even once.
    expect(SSE_KEEPALIVE_MS).toBeLessThan(SERVER_IDLE_TIMEOUT_SEC * 1000)
  })
})

describe("index.ts wiring", () => {
  // The constant being correct is worthless if serve() is not handed it, and
  // index.ts cannot be imported here — importing it binds a port and starts
  // every tailer and sweep. Assert on the source instead.
  const src = readFileSync(join(import.meta.dir, "index.ts"), "utf8")

  test("serve() is passed the derived idleTimeout", () => {
    expect(src).toContain("idleTimeout: SERVER_IDLE_TIMEOUT_SEC")
    expect(src).toContain('from "./timeouts"')
  })

  test("no literal idleTimeout that could drift from the holds", () => {
    expect(src).not.toMatch(/idleTimeout:\s*\d/)
  })
})

describe("Bun.serve idleTimeout — end to end", () => {
  // Longer than a deliberately-too-small timeout, far shorter than the real
  // one, so the two arms below are separated by seconds rather than by the
  // 30s a production long-poll would cost the suite.
  const HOLD_MS = 6_000
  const TOO_SMALL_SEC = 1

  // One response whose body is written only after HOLD_MS — the shape of both
  // /api/chat/poll and /api/chat/events.
  const serveHeld = (idleTimeout: number) => serve({
    port: 0, idleTimeout,
    fetch: () => new Response(new ReadableStream({
      start(controller) {
        setTimeout(() => {
          try { controller.enqueue(new TextEncoder().encode("held-body")); controller.close() }
          catch { /* already torn down */ }
        }, HOLD_MS)
      },
    })),
  })

  test("a timeout under the hold destroys the response; the configured one delivers it", async () => {
    const configured = serveHeld(SERVER_IDLE_TIMEOUT_SEC)
    const tooSmall   = serveHeld(TOO_SMALL_SEC)
    const body = async (port: number) => {
      try { return await fetch(`http://127.0.0.1:${port}/`).then(r => r.text()) }
      catch { return "<connection destroyed>" }   // Bun's client throws; curl sees 200 + 0 bytes
    }
    try {
      // Both in flight at once, so the suite pays HOLD_MS once rather than twice.
      const [configuredBody, tooSmallBody] = await Promise.all([body(configured.port), body(tooSmall.port)])

      // The fix: a hold longer than the old default still delivers.
      expect(configuredBody).toBe("held-body")

      // Test-the-test. Without this arm the assertion above would keep passing
      // even if idleTimeout stopped governing held responses at all, and the
      // regression test would be silently guarding nothing.
      expect(tooSmallBody).toBe("<connection destroyed>")
    } finally {
      configured.stop(true)
      tooSmall.stop(true)
    }
  }, 30_000)
})
