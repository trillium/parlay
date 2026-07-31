// Server URL resolution precedence: PARLAY_SERVER env > persisted
// ~/.parlay/config.json > coded default. PARLAY_STATE_HOME points config.json
// at a tmp dir per test so a real config on the machine running the suite is
// never read (and never written to).

import { afterEach, beforeEach, expect, test } from "bun:test"
import { mkdtempSync, rmSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"

let stateDir: string
let origStateHome: string | undefined
let origServer: string | undefined

beforeEach(() => {
  origStateHome = process.env.PARLAY_STATE_HOME
  origServer = process.env.PARLAY_SERVER
  stateDir = mkdtempSync(join(tmpdir(), "parlay-config-"))
  process.env.PARLAY_STATE_HOME = stateDir
  delete process.env.PARLAY_SERVER
})

afterEach(() => {
  if (origStateHome === undefined) delete process.env.PARLAY_STATE_HOME
  else process.env.PARLAY_STATE_HOME = origStateHome
  if (origServer === undefined) delete process.env.PARLAY_SERVER
  else process.env.PARLAY_SERVER = origServer
  rmSync(stateDir, { recursive: true, force: true })
})

// Re-import fresh each test — config.ts reads state lazily, but re-importing
// keeps this test file independent of module-cache ordering from other files.
async function freshConfig() {
  return import(`./config?t=${Math.random()}`)
}

test("falls back to the coded default when nothing is set", async () => {
  const { serverUrl, serverSource } = await freshConfig()
  expect(serverUrl()).toBe("http://localhost:4242")
  expect(serverSource()).toEqual({ source: "default", url: "http://localhost:4242" })
})

test("persisted config wins over the coded default", async () => {
  const { serverUrl, serverSource, setPersistedServer } = await freshConfig()
  setPersistedServer("http://macbook:31337")
  expect(serverUrl()).toBe("http://macbook:31337")
  expect(serverSource()).toEqual({ source: "config", url: "http://macbook:31337" })
})

test("env var wins over persisted config", async () => {
  const { serverUrl, serverSource, setPersistedServer } = await freshConfig()
  setPersistedServer("http://macbook:31337")
  process.env.PARLAY_SERVER = "http://env-override:9999"
  expect(serverUrl()).toBe("http://env-override:9999")
  expect(serverSource()).toEqual({ source: "env", url: "http://env-override:9999" })
})

test("setPersistedServer trims trailing slashes; clear removes it", async () => {
  const { serverUrl, setPersistedServer, persistedServerUrl } = await freshConfig()
  setPersistedServer("http://mini1:31337///")
  expect(persistedServerUrl()).toBe("http://mini1:31337")
  setPersistedServer(undefined)
  expect(persistedServerUrl()).toBeUndefined()
  expect(serverUrl()).toBe("http://localhost:4242")
})

test("a corrupt config.json is treated as empty, not a crash", async () => {
  const { serverUrl, configFilePath } = await freshConfig()
  const { mkdirSync, writeFileSync } = await import("fs")
  mkdirSync(stateDir, { recursive: true })
  writeFileSync(configFilePath(), "{ not json")
  expect(serverUrl()).toBe("http://localhost:4242")
})
