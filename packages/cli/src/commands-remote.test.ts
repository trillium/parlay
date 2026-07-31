// cmdRemote — get/set/clear the persisted default server URL. State isolated
// to a tmp PARLAY_STATE_HOME per test so this never touches a real
// ~/.parlay/config.json on the machine running the suite.

import { afterEach, beforeEach, expect, test } from "bun:test"
import { mkdtempSync, rmSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { cmdRemote } from "./commands-remote"
import { trapExit } from "./identity-test-harness"

let stateDir: string
let origStateHome: string | undefined
let origServer: string | undefined
let exitTrap: { codes: number[] }
let logs: string[]
let origLog: typeof console.log

beforeEach(() => {
  origStateHome = process.env.PARLAY_STATE_HOME
  origServer = process.env.PARLAY_SERVER
  stateDir = mkdtempSync(join(tmpdir(), "parlay-remote-"))
  process.env.PARLAY_STATE_HOME = stateDir
  delete process.env.PARLAY_SERVER
  exitTrap = trapExit()
  logs = []
  origLog = console.log
  console.log = (...a: unknown[]) => logs.push(a.join(" "))
})

afterEach(() => {
  console.log = origLog
  ;(process as unknown as { exit: typeof process.exit }).exit = process.exit
  if (origStateHome === undefined) delete process.env.PARLAY_STATE_HOME
  else process.env.PARLAY_STATE_HOME = origStateHome
  if (origServer === undefined) delete process.env.PARLAY_SERVER
  else process.env.PARLAY_SERVER = origServer
  rmSync(stateDir, { recursive: true, force: true })
})

test("bare 'remote' reports the coded default when nothing is set", async () => {
  await cmdRemote([])
  expect(logs.join("\n")).toContain("http://localhost:4242")
  expect(logs.join("\n")).toContain("source: default")
})

test("'remote set <url>' persists and 'remote' reflects it", async () => {
  await cmdRemote(["set", "http://mini1.tailnet.ts.net:31337"])
  expect(logs.some(l => l.includes("persisted default server"))).toBe(true)
  logs = []
  await cmdRemote([])
  expect(logs.join("\n")).toContain("http://mini1.tailnet.ts.net:31337")
  expect(logs.join("\n")).toContain("source: config")
})

test("'remote set' rejects an invalid URL (usage error, exit 2)", async () => {
  await expect(cmdRemote(["set", "not-a-url"])).rejects.toThrow()
  expect(exitTrap.codes).toEqual([2])
})

test("'remote set' with no url is a usage error", async () => {
  await expect(cmdRemote(["set"])).rejects.toThrow()
  expect(exitTrap.codes).toEqual([2])
})

test("'remote clear' removes the persisted value, falling back to default", async () => {
  await cmdRemote(["set", "http://macbook:31337"])
  logs = []
  await cmdRemote(["clear"])
  expect(logs.some(l => l.includes("cleared"))).toBe(true)
  logs = []
  await cmdRemote([])
  expect(logs.join("\n")).toContain("http://localhost:4242")
  expect(logs.join("\n")).toContain("source: default")
})

test("PARLAY_SERVER env var still wins over a persisted value", async () => {
  await cmdRemote(["set", "http://macbook:31337"])
  process.env.PARLAY_SERVER = "http://env-wins:9999"
  logs = []
  await cmdRemote([])
  expect(logs.join("\n")).toContain("http://env-wins:9999")
  expect(logs.join("\n")).toContain("source: env")
})

test("unknown subcommand is a usage error", async () => {
  await expect(cmdRemote(["bogus"])).rejects.toThrow()
  expect(exitTrap.codes).toEqual([2])
})
