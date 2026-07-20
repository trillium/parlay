// Integration tests for `parlay identity --reap-ephemeral`: GC of ephemeral
// agents idle past the window, --dry preview, and non-ephemeral immunity.

import { test, expect, beforeAll, afterAll, afterEach } from "bun:test"
import { existsSync, utimesSync } from "fs"
import { join } from "path"
import { cmdIdentity } from "./commands-identity"
import { startHarness, stopHarness, resetHarness, freshHome, seedAgent, trapExit, type Harness } from "./identity-test-harness"

let h: Harness
beforeAll(() => { h = startHarness() })
afterAll(() => stopHarness(h))
afterEach(() => resetHarness(h))

// Backdate a store's identity.md so it falls outside the reap window.
function age(dir: string, hoursAgo: number): void {
  const t = Date.now() / 1000 - hoursAgo * 3600
  utimesSync(join(dir, "identity.md"), t, t)
}

test("--reap-ephemeral --dry lists stale ephemerals without deleting", async () => {
  const home = freshHome(h)
  age(seedAgent(home, "eph-11111111", { ephemeral: true }), 48)
  seedAgent(home, "eph-22222222", { ephemeral: true }) // fresh — kept
  seedAgent(home, "durable", {}) // non-ephemeral — never reaped

  await cmdIdentity(["--reap-ephemeral", "--dry"])

  expect(existsSync(join(home, "eph-11111111"))).toBe(true)
  expect(existsSync(join(home, "eph-22222222"))).toBe(true)
  expect(existsSync(join(home, "durable"))).toBe(true)
})

test("--reap-ephemeral deletes stale ephemerals, keeps fresh + non-ephemeral", async () => {
  const home = freshHome(h)
  age(seedAgent(home, "eph-33333333", { ephemeral: true }), 100) // stale
  seedAgent(home, "eph-44444444", { ephemeral: true }) // fresh
  age(seedAgent(home, "durable-agent", {}), 100) // non-ephemeral, old

  await cmdIdentity(["--reap-ephemeral", "--older-than", "24h"])

  expect(existsSync(join(home, "eph-33333333"))).toBe(false) // reaped
  expect(existsSync(join(home, "eph-44444444"))).toBe(true) // fresh, kept
  expect(existsSync(join(home, "durable-agent"))).toBe(true) // non-ephemeral, kept
})

test("--reap-ephemeral honors a custom --older-than window", async () => {
  const home = freshHome(h)
  age(seedAgent(home, "eph-55555555", { ephemeral: true }), 2) // 2h old
  // With a 1h window, a 2h-idle ephemeral is stale.
  await cmdIdentity(["--reap-ephemeral", "--older-than", "1h"])
  expect(existsSync(join(home, "eph-55555555"))).toBe(false)
})

test("--reap-ephemeral rejects a malformed --older-than", async () => {
  const home = freshHome(h)
  seedAgent(home, "eph-66666666", { ephemeral: true })
  const { codes } = trapExit()
  expect(cmdIdentity(["--reap-ephemeral", "--older-than", "soon"])).rejects.toThrow(/process\.exit/)
  await Promise.resolve()
  expect(codes).toContain(2)
  expect(existsSync(join(home, "eph-66666666"))).toBe(true) // nothing deleted on error
})
