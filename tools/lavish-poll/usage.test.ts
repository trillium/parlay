// End-to-end usage handling: a rejected argument has to reach the process exit
// code, not just the parser. args.test.ts covers parsePollArgs directly; this
// file proves index.ts wires UsageError to exit 1 rather than swallowing it.

import { test, expect, describe } from "bun:test"
import { runBridge, BRIDGE } from "./harness"

describe("usage errors reach the exit code", () => {
  test("an unknown flag exits 1 with the reason and the usage line on stderr", async () => {
    const r = await runBridge({ args: ["doc.md", "--dry-run"] })
    expect(r.code).toBe(1)
    expect(r.stderr).toContain("unknown flag --dry-run")
    expect(r.stderr).toContain("usage:")
    // Nothing should have been emitted on stdout — a caller parsing the last
    // JSON line must not find a success record next to a failure exit.
    expect(r.stdout.trim()).toBe("")
  })

  test("a bad --timeout-ms exits 1 rather than polling forever", async () => {
    const r = await runBridge({ args: ["doc.md", "--timeout-ms", "abc"] })
    expect(r.code).toBe(1)
    expect(r.stderr).toContain("must be a positive number")
  })

  test("a missing file exits 1", async () => {
    const r = await runBridge({ args: ["--timeout-ms", "500"] })
    expect(r.code).toBe(1)
    expect(r.stderr).toContain("a file argument is required")
  })

  test("a missing agentId/parlayUrl exits 1", async () => {
    const proc = Bun.spawn(["bun", BRIDGE], { stdout: "pipe", stderr: "pipe" })
    const [stderr, code] = await Promise.all([new Response(proc.stderr).text(), proc.exited])
    expect(code).toBe(1)
    expect(stderr).toContain("agentId and parlayUrl are required")
  })
})
