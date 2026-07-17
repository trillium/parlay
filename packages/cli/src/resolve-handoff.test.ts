// Proves the create→submit death-window recovery primitive: after a handoff bead is
// created but before `identity --submit` runs, the id can always be recovered from the
// store, so a bare `identity --submit` (no id) is never stranded.

import { test, expect, afterEach } from "bun:test"
import { mkdtempSync, writeFileSync, chmodSync, rmSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { resolveCurrentHandoff } from "./resolve-handoff"

const dirs: string[] = []
const origPath = process.env.PATH

afterEach(() => {
  process.env.PATH = origPath
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true })
})

// Install a fake `<store>` executable that answers `show --current --json` with the
// given body, then prepend it to PATH so resolveCurrentHandoff shells out to it.
function stubStore(store: string, jsonBody: string, status = 0): void {
  const dir = mkdtempSync(join(tmpdir(), "parlay-handoff-"))
  dirs.push(dir)
  const bin = join(dir, store)
  const heredoc = jsonBody.replace(/'/g, "'\\''")
  writeFileSync(bin, `#!/usr/bin/env bash\nprintf '%s' '${heredoc}'\nexit ${status}\n`)
  chmodSync(bin, 0o755)
  process.env.PATH = `${dir}:${origPath}`
}

test("resolves the current open handoff id (a stranded create is recoverable)", () => {
  stubStore("handoff", JSON.stringify([{ id: "handoff-1bk", title: "Mayor shutdown" }]))
  expect(resolveCurrentHandoff()).toBe("handoff-1bk")
})

test("accepts a single-object (non-array) store response", () => {
  stubStore("handoff", JSON.stringify({ id: "handoff-xyz" }))
  expect(resolveCurrentHandoff()).toBe("handoff-xyz")
})

test("returns undefined when the store reports no active handoff (empty array)", () => {
  stubStore("handoff", "[]")
  expect(resolveCurrentHandoff()).toBeUndefined()
})

test("returns undefined on a non-zero store exit (store unavailable)", () => {
  stubStore("handoff", "", 1)
  expect(resolveCurrentHandoff()).toBeUndefined()
})

test("returns undefined on unparseable store output — never throws", () => {
  stubStore("handoff", "not json at all")
  expect(resolveCurrentHandoff()).toBeUndefined()
})

test("honors a non-default store name (id prefix drives the store CLI)", () => {
  stubStore("myhandoff", JSON.stringify([{ id: "myhandoff-7" }]))
  expect(resolveCurrentHandoff("myhandoff")).toBe("myhandoff-7")
})

test("missing store binary resolves to undefined instead of crashing", () => {
  const dir = mkdtempSync(join(tmpdir(), "parlay-empty-"))
  dirs.push(dir)
  process.env.PATH = dir // no `handoff` anywhere
  expect(resolveCurrentHandoff()).toBeUndefined()
})
