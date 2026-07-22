// Tests for identity --park: pin the handoff pointer (exactly like --submit) then
// shut down WITHOUT --reboot, leaving the bead OPEN. The middle of the three-exit
// model (decision-q3x): --submit restarts, --park pauses, --complete terminates.
//
// SAFETY: --park spawns `context-reset`, which — under CLAUDECODE=1 — walks up to
// the ancestor `claude` process and kills it. These tests DELETE CLAUDECODE for the
// child (so context-reset exits early, killing nothing) and pass --dry as a second
// guard. Never run the non-dry park path from inside a live session.

import { test, expect, beforeAll, afterAll, afterEach } from "bun:test"
import { readFileSync } from "fs"
import { join } from "path"
import { cmdIdentity } from "./commands-identity"
import { startHarness, stopHarness, resetHarness, freshHome, seedAgent, type Harness } from "./identity-test-harness"

let h: Harness
beforeAll(() => { h = startHarness() })
afterAll(() => stopHarness(h))
afterEach(() => resetHarness(h))

// Run cmdIdentity with CLAUDECODE stripped from the env so the spawned
// context-reset finds no live session and exits before touching any process.
async function parkSafely(args: string[]): Promise<string[]> {
  const logs: string[] = []
  const origLog = console.log
  const origCC = process.env.CLAUDECODE
  console.log = (...a: unknown[]) => { logs.push(a.join(" ")) }
  delete process.env.CLAUDECODE
  try {
    await cmdIdentity(args)
  } finally {
    console.log = origLog
    if (origCC === undefined) delete process.env.CLAUDECODE
    else process.env.CLAUDECODE = origCC
  }
  return logs
}

test("--park with an explicit handoff id pins the pointer and reports shutdown WITHOUT restart, bead OPEN", async () => {
  const home = freshHome(h)
  seedAgent(home, "worker")
  process.env.PARLAY_AGENT_ID = "worker"

  const logs = await parkSafely(["--park", "handoff-abc", "--dry"])

  // Pointer pinned into identity.md just below the header — same shape as --submit.
  const raw = readFileSync(join(home, "worker", "identity.md"), "utf8")
  expect(raw).toContain("> 📎 Handoff: handoff-abc — run `handoff show handoff-abc` for full session state")

  // Messaging is park-specific: parked, bead OPEN, no restart.
  const out = logs.join("\n")
  expect(out).toContain("identity parked for worker")
  expect(out).toContain("handoff-abc")
  expect(out).toContain("OPEN")
  expect(out).toContain("WITHOUT restart")
  // Never claims a reset/restart the way --submit does.
  expect(out).not.toContain("context reset")

  delete process.env.PARLAY_AGENT_ID
})

test("--park is identity-only — scratchpad --park is rejected", async () => {
  const home = freshHome(h)
  seedAgent(home, "worker")
  process.env.PARLAY_AGENT_ID = "worker"
  const { cmdScratchpad } = await import("./commands-identity")
  const codes: number[] = []
  const origExit = process.exit
  ;(process as unknown as { exit: (c?: number) => never }).exit = ((c?: number) => {
    codes.push(c ?? 0); throw new Error(`exit ${c ?? 0}`)
  }) as never
  const origCC = process.env.CLAUDECODE
  delete process.env.CLAUDECODE
  try {
    await cmdScratchpad(["--park", "handoff-abc"])
  } catch { /* die() throws via trapped exit */ } finally {
    ;(process as unknown as { exit: typeof process.exit }).exit = origExit
    if (origCC === undefined) delete process.env.CLAUDECODE
    else process.env.CLAUDECODE = origCC
    delete process.env.PARLAY_AGENT_ID
  }
  expect(codes.length).toBeGreaterThan(0)
})
