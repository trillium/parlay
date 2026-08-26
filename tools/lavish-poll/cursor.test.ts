// The cursor must advance as soon as a message is CONSUMED, before any
// filtering on role or text. Pre-fix it advanced only inside the
// `role === "user" && text != null` branch, so an agent reply — which carries
// an id — left `after=` unchanged. The next iteration issued the identical
// request, got the same message back, and span until the deadline.

import { test, expect, describe } from "bun:test"
import { readdirSync, readFileSync, mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { runBridge, onceThenStall } from "./harness"

describe("cursor advancement", () => {
  test("an agent reply advances the cursor instead of being re-fetched forever", async () => {
    const parlay = onceThenStall({ id: "m1", role: "agent", text: "agent says hi" })
    try {
      const r = await runBridge({
        args: ["doc.md", "--timeout-ms", "1500"],
        parlayUrl: parlay.url,
      })
      expect(r.code).toBe(0)
      expect(parlay.seen.length).toBeGreaterThanOrEqual(2)
      expect(parlay.seen[1]).toBe("m1") // must NOT repeat after=""
      expect(parlay.seen.filter(a => a === "").length).toBe(1) // and must not spin
    } finally {
      parlay.server.stop(true)
    }
  })

  test("a user message with null text advances the cursor rather than blocking on itself", async () => {
    const parlay = onceThenStall({ id: "m9", role: "user" }) // no text
    try {
      await runBridge({ args: ["doc.md", "--timeout-ms", "1500"], parlayUrl: parlay.url })
      expect(parlay.seen.length).toBeGreaterThanOrEqual(2)
      expect(parlay.seen[1]).toBe("m9")
    } finally {
      parlay.server.stop(true)
    }
  })

  test("the advanced cursor is persisted so a re-arm resumes past the message", async () => {
    // robots-nm8: emit() exits the process and the tool's own next_step tells
    // the agent to re-run, so a cursor that only lives in memory re-delivers
    // the same message on every re-arm.
    const runtime = mkdtempSync(join(tmpdir(), "lavish-poll-cursor-"))
    const parlay = onceThenStall({ id: "persisted-1", role: "agent", text: "x" })
    try {
      await runBridge({
        args: ["doc.md", "--timeout-ms", "1200"],
        parlayUrl: parlay.url,
        runtime,
      })
      const cursors = readdirSync(runtime).filter(f => f.endsWith(".cursor"))
      expect(cursors.length).toBe(1)
      expect(readFileSync(join(runtime, cursors[0]!), "utf8").trim()).toBe("persisted-1")
    } finally {
      parlay.server.stop(true)
    }
  })

  test("a different file gets a different cursor file", async () => {
    // The key is sha256(agentId + "\0" + file), so two documents polled by the
    // same agent must not share a resume point.
    const runtime = mkdtempSync(join(tmpdir(), "lavish-poll-cursor-"))
    const a = onceThenStall({ id: "from-a", role: "agent", text: "x" })
    try {
      await runBridge({ args: ["a.md", "--timeout-ms", "900"], parlayUrl: a.url, runtime })
    } finally {
      a.server.stop(true)
    }
    const b = onceThenStall({ id: "from-b", role: "agent", text: "x" })
    try {
      await runBridge({ args: ["b.md", "--timeout-ms", "900"], parlayUrl: b.url, runtime })
    } finally {
      b.server.stop(true)
    }
    const cursors = readdirSync(runtime).filter(f => f.endsWith(".cursor")).sort()
    expect(cursors.length).toBe(2)
    const contents = cursors.map(f => readFileSync(join(runtime, f), "utf8").trim()).sort()
    expect(contents).toEqual(["from-a", "from-b"])
  })
})
