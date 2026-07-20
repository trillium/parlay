// Covers the identity.md pointer reader used by the create→submit chat guard.
// (The warn condition itself is exercised end-to-end through detectUnsubmittedHandoff
// in resolve-handoff.test.ts; this pins down the file-read half.)

import { test, expect, afterEach } from "bun:test"
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { pinnedHandoffId } from "./say-guard"

const dirs: string[] = []
const origHome = process.env.PARLAY_AGENT_HOME

afterEach(() => {
  if (origHome === undefined) delete process.env.PARLAY_AGENT_HOME
  else process.env.PARLAY_AGENT_HOME = origHome
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true })
})

// Write an identity.md for `agent` under a fresh PARLAY_AGENT_HOME and point env at it.
function seedIdentity(agent: string, body: string): void {
  const home = mkdtempSync(join(tmpdir(), "parlay-guard-home-"))
  dirs.push(home)
  mkdirSync(join(home, agent), { recursive: true })
  writeFileSync(join(home, agent, "identity.md"), body)
  process.env.PARLAY_AGENT_HOME = home
}

test("reads a pinned handoff pointer from identity.md", () => {
  seedIdentity("mayor",
    "---\nid: mayor\n---\n# Identity — mayor\n\n> 📎 Handoff: handoff-1bk — run `handoff show handoff-1bk` for full session state\n")
  expect(pinnedHandoffId("mayor")).toBe("handoff-1bk")
})

test("returns undefined when identity.md has no pointer", () => {
  seedIdentity("mayor", "---\nid: mayor\n---\n# Identity — mayor\n\n- [2026-07-20] some fact\n")
  expect(pinnedHandoffId("mayor")).toBeUndefined()
})

test("returns undefined when identity.md is absent", () => {
  const home = mkdtempSync(join(tmpdir(), "parlay-guard-empty-"))
  dirs.push(home)
  process.env.PARLAY_AGENT_HOME = home // no <agent>/identity.md
  expect(pinnedHandoffId("mayor")).toBeUndefined()
})
