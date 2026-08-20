import { describe, test, expect, beforeAll, afterAll } from "bun:test"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { resolveToken, flatFilePath, keychainServiceName } from "./index"

let scratch: string
beforeAll(() => { scratch = mkdtempSync(join(tmpdir(), "ccjuggler-test-")) })
afterAll(() => { rmSync(scratch, { recursive: true, force: true }) })

describe("flatFilePath", () => {
  test("returns ~/.ccjuggler/<account>/.oauth-token", () => {
    const p = flatFilePath("acc1")
    expect(p).toMatch(/\.ccjuggler[/\\]acc1[/\\]\.oauth-token$/)
  })
})

describe("keychainServiceName", () => {
  test("prefixes with ccjuggler-", () => {
    expect(keychainServiceName("acc2")).toBe("ccjuggler-acc2")
  })
})

describe("resolveToken (flat file)", () => {
  test("resolves token from flat file when keychain misses", async () => {
    const dir = join(scratch, ".ccjuggler", "test-acc")
    mkdirSync(dir, { recursive: true })
    writeFileSync(join(dir, ".oauth-token"), "tok-from-file")
    const token = await resolveToken("test-acc", { home: scratch })
    expect(token).toBe("tok-from-file")
  })

  test("trims trailing whitespace from flat file", async () => {
    const dir = join(scratch, ".ccjuggler", "trim-acc")
    mkdirSync(dir, { recursive: true })
    writeFileSync(join(dir, ".oauth-token"), "tok-trimmed\n")
    const token = await resolveToken("trim-acc", { home: scratch })
    expect(token).toBe("tok-trimmed")
  })

  test("throws with informative message when both sources miss", async () => {
    const home = join(scratch, "empty-home")
    mkdirSync(home, { recursive: true })
    await expect(resolveToken("ghost-acc", { home })).rejects.toThrow(
      "no token found for account 'ghost-acc'"
    )
  })

  test("error message names both sources tried", async () => {
    const home = join(scratch, "empty-home2")
    mkdirSync(home, { recursive: true })
    await expect(resolveToken("ghost-acc", { home })).rejects.toThrow("ccjuggler-ghost-acc")
  })
})
