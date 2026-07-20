// Integration tests for the ephemeral identity seed path: --mint-ephemeral
// (generate + seed store) and --register --ephemeral (frontmatter marker +
// context.json). Rename and reap live in their own *.test.ts files.

import { test, expect, beforeAll, afterAll, afterEach } from "bun:test"
import { readFileSync, existsSync } from "fs"
import { join } from "path"
import { cmdIdentity, readFrontmatter } from "./commands-identity"
import { startHarness, stopHarness, resetHarness, freshHome, type Harness } from "./identity-test-harness"

let h: Harness
beforeAll(() => { h = startHarness() })
afterAll(() => stopHarness(h))
afterEach(() => resetHarness(h))

test("--mint-ephemeral seeds a fresh eph-* store (identity.md + context.json) and prints id/name/color", async () => {
  const home = freshHome(h)
  const logs: string[] = []
  const origLog = console.log
  console.log = (...a: unknown[]) => { logs.push(a.join(" ")) }
  try {
    await cmdIdentity(["--mint-ephemeral", "--cwd", "/tmp/demo", "--model", "sonnet"])
  } finally {
    console.log = origLog
  }
  const line = logs.at(-1) ?? ""
  const parts = line.split("\t")
  expect(parts.length).toBe(3) // tab-separated id / name / color
  const [id, name, color] = parts
  expect(id).toMatch(/^eph-[0-9a-f]{8}$/)
  expect(name).toBe(`Agent ${id.slice(4).toUpperCase()}`) // name may contain a space
  expect(color).toMatch(/^#[0-9a-f]{6}$/)
  // Dir was created even though it did not exist beforehand.
  const fm = readFrontmatter(join(home, id, "identity.md"))
  expect(fm.id).toBe(id)
  expect(fm.ephemeral).toBe("true")
  expect(fm.cwd).toBe("/tmp/demo")
  expect(fm.model).toBe("sonnet")
  const ctx = JSON.parse(readFileSync(join(home, id, "context.json"), "utf8"))
  expect(ctx.id).toBe(id)
  expect(ctx.name).toBe(name)
  expect(ctx.color).toBe(color)
})

test("--mint-ephemeral orders the ephemeral field AFTER cwd in the frontmatter", async () => {
  freshHome(h)
  const logs: string[] = []
  const origLog = console.log
  console.log = (...a: unknown[]) => { logs.push(a.join(" ")) }
  try {
    await cmdIdentity(["--mint-ephemeral", "--cwd", "/tmp/x"])
  } finally {
    console.log = origLog
  }
  const id = (logs.at(-1) ?? "").split("\t")[0]
  const raw = readFileSync(join(process.env.PARLAY_AGENT_HOME!, id, "identity.md"), "utf8")
  const fmBlock = raw.match(/^---\n([\s\S]*?)\n---\n/)![1]
  const keys = fmBlock.split("\n").map((l) => l.split(":")[0].trim())
  expect(keys.indexOf("ephemeral")).toBeGreaterThan(keys.indexOf("cwd"))
})

test("--register --ephemeral writes identity.md frontmatter with ephemeral: true + context.json", async () => {
  const home = freshHome(h)
  await cmdIdentity(["--register", "--agent", "eph-a1b2c3d4", "--name", "Agent A1B2C3D4", "--color", "#a1b2c3", "--cwd", "/tmp/x", "--ephemeral"])
  const fm = readFrontmatter(join(home, "eph-a1b2c3d4", "identity.md"))
  expect(fm.ephemeral).toBe("true")
  expect(fm.id).toBe("eph-a1b2c3d4")
  const ctx = JSON.parse(readFileSync(join(home, "eph-a1b2c3d4", "context.json"), "utf8"))
  expect(ctx).toEqual({ id: "eph-a1b2c3d4", name: "Agent A1B2C3D4", color: "#a1b2c3" })
})

test("--register without --ephemeral does NOT add the ephemeral field but still writes context.json", async () => {
  const home = freshHome(h)
  await cmdIdentity(["--register", "--agent", "worker", "--name", "Worker", "--color", "#010203"])
  const fm = readFrontmatter(join(home, "worker", "identity.md"))
  expect(fm.ephemeral).toBeUndefined()
  expect(existsSync(join(home, "worker", "context.json"))).toBe(true)
})
