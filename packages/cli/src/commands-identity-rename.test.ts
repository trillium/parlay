// Integration tests for `parlay identity --rename <old> --to <new>`: store move,
// context.json/frontmatter id rewrite, override application, reincarnations log,
// server re-registration, the clobber guard, and --preserve.

import { test, expect, beforeAll, afterAll, afterEach } from "bun:test"
import { readFileSync, existsSync } from "fs"
import { join } from "path"
import { cmdIdentity, readFrontmatter } from "./commands-identity"
import { startHarness, stopHarness, resetHarness, freshHome, seedAgent, trapExit, type Harness } from "./identity-test-harness"

let h: Harness
beforeAll(() => { h = startHarness() })
afterAll(() => stopHarness(h))
afterEach(() => resetHarness(h))

test("--rename moves the store dir, updates context.json + identity.md id, logs reincarnation, re-registers", async () => {
  const home = freshHome(h)
  seedAgent(home, "old-id", { name: "Old", color: "#ff0000", reincarnation: true })

  await cmdIdentity(["--rename", "old-id", "--to", "new-id"])

  expect(existsSync(join(home, "old-id"))).toBe(false)
  expect(existsSync(join(home, "new-id"))).toBe(true)

  const ctx = JSON.parse(readFileSync(join(home, "new-id", "context.json"), "utf8"))
  expect(ctx.id).toBe("new-id")
  expect(ctx.name).toBe("Old") // preserved when no override

  const fm = readFrontmatter(join(home, "new-id", "identity.md"))
  expect(fm.id).toBe("new-id")

  const log = readFileSync(join(home, "new-id", "reincarnations.log"), "utf8")
  expect(log.split("\n")[0]).toMatch(/^\[.+\] renamed from old-id to new-id$/)
  expect(log).toContain('"agent":"old-id"') // original content preserved below

  expect(h.registerBodies.length).toBe(1)
  expect(h.registerBodies[0]).toMatchObject({ id: "new-id", name: "Old", color: "#ff0000" })
})

test("--rename applies --name/--color overrides to both context.json and frontmatter", async () => {
  const home = freshHome(h)
  seedAgent(home, "old-id", { name: "Old", color: "#ff0000" })
  await cmdIdentity(["--rename", "old-id", "--to", "new-id", "--name", "Renamed", "--color", "#00ff00"])
  const ctx = JSON.parse(readFileSync(join(home, "new-id", "context.json"), "utf8"))
  expect(ctx).toMatchObject({ id: "new-id", name: "Renamed", color: "#00ff00" })
  const fm = readFrontmatter(join(home, "new-id", "identity.md"))
  expect(fm.name).toBe("Renamed")
  expect(fm.color).toBe("#00ff00")
})

test("--rename errors (and does not move) when the target id already exists", async () => {
  const home = freshHome(h)
  seedAgent(home, "old-id")
  seedAgent(home, "taken")
  const { codes } = trapExit()
  expect(cmdIdentity(["--rename", "old-id", "--to", "taken"])).rejects.toThrow(/process\.exit/)
  expect(existsSync(join(home, "old-id"))).toBe(true)
  expect(existsSync(join(home, "taken"))).toBe(true)
  expect(h.registerBodies.length).toBe(0)
  await Promise.resolve()
  expect(codes).toContain(2)
})

test("--rename errors when --to is missing", async () => {
  const home = freshHome(h)
  seedAgent(home, "old-id")
  const { codes } = trapExit()
  expect(cmdIdentity(["--rename", "old-id"])).rejects.toThrow(/process\.exit/)
  await Promise.resolve()
  expect(codes).toContain(2)
  expect(existsSync(join(home, "old-id"))).toBe(true)
})

test("--rename --preserve clears the ephemeral marker from frontmatter", async () => {
  const home = freshHome(h)
  seedAgent(home, "eph-deadbeef", { ephemeral: true })
  await cmdIdentity(["--rename", "eph-deadbeef", "--to", "durable", "--preserve"])
  const fm = readFrontmatter(join(home, "durable", "identity.md"))
  expect(fm.ephemeral).toBeUndefined()
  expect(fm.id).toBe("durable")
})

test("--rename without --preserve keeps the ephemeral marker", async () => {
  const home = freshHome(h)
  seedAgent(home, "eph-cafef00d", { ephemeral: true })
  await cmdIdentity(["--rename", "eph-cafef00d", "--to", "eph-newname1"])
  const fm = readFrontmatter(join(home, "eph-newname1", "identity.md"))
  expect(fm.ephemeral).toBe("true")
})
