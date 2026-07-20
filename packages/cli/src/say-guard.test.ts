// Covers the identity.md pointer reader and session-start sentinel used by the
// create→submit chat guard. The warn condition itself is exercised end-to-end
// through detectUnsubmittedHandoff in resolve-handoff.test.ts.

import { test, expect, afterEach } from "bun:test"
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, existsSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { pinnedHandoffId, readSessionStartMs, writeSessionStartOnce } from "./say-guard"

const dirs: string[] = []
const origHome = process.env.PARLAY_AGENT_HOME

afterEach(() => {
  if (origHome === undefined) delete process.env.PARLAY_AGENT_HOME
  else process.env.PARLAY_AGENT_HOME = origHome
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true })
})

function freshAgentHome(): string {
  const home = mkdtempSync(join(tmpdir(), "parlay-guard-home-"))
  dirs.push(home)
  process.env.PARLAY_AGENT_HOME = home
  return home
}

// Write an identity.md for `agent` under a fresh PARLAY_AGENT_HOME and point env at it.
function seedIdentity(agent: string, body: string): void {
  const home = freshAgentHome()
  mkdirSync(join(home, agent), { recursive: true })
  writeFileSync(join(home, agent, "identity.md"), body)
}

// ── pinnedHandoffId ───────────────────────────────────────────────────────────────

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

// ── readSessionStartMs / writeSessionStartOnce ────────────────────────────────────

test("readSessionStartMs returns undefined when session-start file is absent", () => {
  freshAgentHome()
  expect(readSessionStartMs("brain-dev")).toBeUndefined()
})

test("readSessionStartMs parses epoch-seconds from session-start file", () => {
  const home = freshAgentHome()
  const epochSec = Math.floor(Date.now() / 1000) - 100
  mkdirSync(join(home, "brain-dev"), { recursive: true })
  writeFileSync(join(home, "brain-dev", "session-start"), epochSec.toString() + "\n")
  expect(readSessionStartMs("brain-dev")).toBe(epochSec * 1000)
})

test("writeSessionStartOnce creates the file if absent", () => {
  const home = freshAgentHome()
  mkdirSync(join(home, "brain-dev"), { recursive: true })
  const before = Math.floor(Date.now() / 1000)
  writeSessionStartOnce("brain-dev")
  const after  = Math.floor(Date.now() / 1000)
  const file = join(home, "brain-dev", "session-start")
  expect(existsSync(file)).toBe(true)
  const written = parseInt(require("fs").readFileSync(file, "utf8").trim(), 10)
  expect(written).toBeGreaterThanOrEqual(before)
  expect(written).toBeLessThanOrEqual(after)
})

test("writeSessionStartOnce does NOT overwrite an existing sentinel", () => {
  const home = freshAgentHome()
  mkdirSync(join(home, "brain-dev"), { recursive: true })
  const old = "1720000000"
  writeFileSync(join(home, "brain-dev", "session-start"), old)
  writeSessionStartOnce("brain-dev")
  const still = require("fs").readFileSync(join(home, "brain-dev", "session-start"), "utf8").trim()
  expect(still).toBe(old)
})
