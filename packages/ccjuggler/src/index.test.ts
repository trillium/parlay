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

describe("account name is an opaque string", () => {
  test("'2' and 'acc2' are distinct accounts — different keychain service names", () => {
    expect(keychainServiceName("2")).toBe("ccjuggler-2")
    expect(keychainServiceName("acc2")).toBe("ccjuggler-acc2")
    expect(keychainServiceName("2")).not.toBe(keychainServiceName("acc2"))
  })

  test("flat file paths differ for '2' vs 'acc2'", () => {
    expect(flatFilePath("2")).not.toBe(flatFilePath("acc2"))
    expect(flatFilePath("2")).toMatch(/\.ccjuggler[/\\]2[/\\]\.oauth-token$/)
    expect(flatFilePath("acc2")).toMatch(/\.ccjuggler[/\\]acc2[/\\]\.oauth-token$/)
  })

  test("token stored under 'xacc2' is not found when requested as 'x2'", async () => {
    // Use fictional names that are guaranteed absent from the system keychain.
    // This tests the core invariant: a token stored under name A is not retrievable
    // under name B, which is the root cause of the acc2/2 mismatch bug.
    const home = join(scratch, "mismatch-home")
    mkdirSync(join(home, ".ccjuggler", "xacc2"), { recursive: true })
    writeFileSync(join(home, ".ccjuggler", "xacc2", ".oauth-token"), "tok-xacc2")
    // correct name resolves via flat file
    await expect(resolveToken("xacc2", { home })).resolves.toBe("tok-xacc2")
    // wrong name misses — the config-file/keychain name-mismatch class of bug
    await expect(resolveToken("x2", { home })).rejects.toThrow("no token found for account 'x2'")
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
