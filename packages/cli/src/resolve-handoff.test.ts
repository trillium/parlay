// Proves the create→submit death-window recovery primitives:
//   resolveCurrentHandoff  — a bare `identity --submit` always finds the agent's newest
//                            OPEN handoff, so a stranded create is never lost.
//   detectUnsubmittedHandoff — a chat send can tell whether it is landing inside the
//                            create→submit window (open handoff not yet pinned).

import { test, expect, afterEach } from "bun:test"
import { mkdtempSync, writeFileSync, chmodSync, rmSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { resolveCurrentHandoff, detectUnsubmittedHandoff } from "./resolve-handoff"

const dirs: string[] = []
const origPath = process.env.PATH
const origAgent = process.env.PARLAY_AGENT_ID

afterEach(() => {
  process.env.PATH = origPath
  if (origAgent === undefined) delete process.env.PARLAY_AGENT_ID
  else process.env.PARLAY_AGENT_ID = origAgent
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true })
})

// Install a fake `<store>` executable. It dispatches on the FIRST arg:
//   list  → answers with `listBody`
//   show  → answers with `showBody` (the legacy --current fallback)
// Each answer is an env-selected JSON string; a status of 1 simulates the verb failing.
// Missing bodies exit non-zero so the resolver falls through the query chain naturally.
function stubStore(
  store: string,
  bodies: { list?: { json: string; status?: number }; show?: { json: string; status?: number } },
): void {
  const dir = mkdtempSync(join(tmpdir(), "parlay-handoff-"))
  dirs.push(dir)
  const bin = join(dir, store)
  const esc = (s: string) => s.replace(/'/g, "'\\''")
  const listJson = bodies.list ? esc(bodies.list.json) : ""
  const listStatus = bodies.list?.status ?? (bodies.list ? 0 : 3)
  const showJson = bodies.show ? esc(bodies.show.json) : ""
  const showStatus = bodies.show?.status ?? (bodies.show ? 0 : 3)
  writeFileSync(bin,
    `#!/usr/bin/env bash\n` +
    `case "$1" in\n` +
    `  list) printf '%s' '${listJson}'; exit ${listStatus};;\n` +
    `  show) printf '%s' '${showJson}'; exit ${showStatus};;\n` +
    `  *) exit 3;;\n` +
    `esac\n`,
  )
  chmodSync(bin, 0o755)
  process.env.PATH = `${dir}:${origPath}`
}

// ── resolveCurrentHandoff: newest-open-for-agent, with fallbacks ──────────────────

test("resolves the agent's newest open handoff via list (a stranded create is recoverable)", () => {
  stubStore("handoff", { list: { json: JSON.stringify([{ id: "handoff-1bk", status: "open" }]) } })
  expect(resolveCurrentHandoff("handoff", "mayor")).toBe("handoff-1bk")
})

test("returns the FIRST row of a multi-row list (store sorts newest-first)", () => {
  stubStore("handoff", { list: { json: JSON.stringify([
    { id: "handoff-new", status: "in_progress" },
    { id: "handoff-old", status: "open" },
  ]) } })
  expect(resolveCurrentHandoff("handoff", "mayor")).toBe("handoff-new")
})

test("accepts a single-object (non-array) list response", () => {
  stubStore("handoff", { list: { json: JSON.stringify({ id: "handoff-xyz", status: "open" }) } })
  expect(resolveCurrentHandoff("handoff", "mayor")).toBe("handoff-xyz")
})

test("falls back to store 'current' when list yields nothing open", () => {
  // list exits non-zero (verb unsupported); show --current answers with an OPEN row.
  stubStore("handoff", {
    list: { json: "", status: 1 },
    show: { json: JSON.stringify([{ id: "handoff-cur", status: "in_progress" }]) },
  })
  expect(resolveCurrentHandoff("handoff", "mayor")).toBe("handoff-cur")
})

test("does NOT return a CLOSED 'current' handoff (last-touched but done)", () => {
  stubStore("handoff", {
    list: { json: "[]" },                                    // nothing open
    show: { json: JSON.stringify([{ id: "handoff-done", status: "closed" }]) },
  })
  expect(resolveCurrentHandoff("handoff", "mayor")).toBeUndefined()
})

test("returns undefined when nothing is open anywhere", () => {
  stubStore("handoff", { list: { json: "[]" }, show: { json: "[]" } })
  expect(resolveCurrentHandoff("handoff", "mayor")).toBeUndefined()
})

test("returns undefined on unparseable store output — never throws", () => {
  stubStore("handoff", { list: { json: "not json" }, show: { json: "also not json" } })
  expect(resolveCurrentHandoff("handoff", "mayor")).toBeUndefined()
})

test("honors a non-default store name (id prefix drives the store CLI)", () => {
  stubStore("myhandoff", { list: { json: JSON.stringify([{ id: "myhandoff-7", status: "open" }]) } })
  expect(resolveCurrentHandoff("myhandoff", "mayor")).toBe("myhandoff-7")
})

test("missing store binary resolves to undefined instead of crashing", () => {
  const dir = mkdtempSync(join(tmpdir(), "parlay-empty-"))
  dirs.push(dir)
  process.env.PATH = dir // no `handoff` anywhere
  expect(resolveCurrentHandoff("handoff", "mayor")).toBeUndefined()
})

test("agent defaults to PARLAY_AGENT_ID when not passed explicitly", () => {
  process.env.PARLAY_AGENT_ID = "mayor"
  stubStore("handoff", { list: { json: JSON.stringify([{ id: "handoff-env", status: "open" }]) } })
  expect(resolveCurrentHandoff()).toBe("handoff-env")
})

// ── detectUnsubmittedHandoff: the say/reply warn condition ────────────────────────

test("detects an open handoff NOT yet pinned as unsubmitted (warn fires)", () => {
  stubStore("handoff", { list: { json: JSON.stringify([{ id: "handoff-1bk", status: "open" }]) } })
  // No pointer pinned in identity.md yet → the create→submit window is open.
  expect(detectUnsubmittedHandoff(undefined, "handoff", "mayor")).toBe("handoff-1bk")
})

test("does NOT flag when the open handoff is already the pinned pointer (submitted)", () => {
  stubStore("handoff", { list: { json: JSON.stringify([{ id: "handoff-1bk", status: "open" }]) } })
  // identity.md already pins handoff-1bk → submit happened; no warning.
  expect(detectUnsubmittedHandoff("handoff-1bk", "handoff", "mayor")).toBeUndefined()
})

test("flags a DIFFERENT open handoff even when an older pointer is pinned", () => {
  stubStore("handoff", { list: { json: JSON.stringify([{ id: "handoff-new", status: "open" }]) } })
  // A prior session pinned handoff-old; a fresh unsubmitted handoff-new is open now.
  expect(detectUnsubmittedHandoff("handoff-old", "handoff", "mayor")).toBe("handoff-new")
})

test("no warning when nothing is open (clean state)", () => {
  stubStore("handoff", { list: { json: "[]" }, show: { json: "[]" } })
  expect(detectUnsubmittedHandoff(undefined, "handoff", "mayor")).toBeUndefined()
})

test("tolerates whitespace around the pinned pointer id", () => {
  stubStore("handoff", { list: { json: JSON.stringify([{ id: "handoff-1bk", status: "open" }]) } })
  expect(detectUnsubmittedHandoff("  handoff-1bk  ", "handoff", "mayor")).toBeUndefined()
})
