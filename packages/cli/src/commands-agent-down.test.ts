// cmdAgentDown — general-purpose deregistration for any agent channel (not
// just git-worktree variants; see commands-variant.ts's teardown for the
// narrower case). Drives against a throwaway Bun.serve that mimics the real
// /api/chat/unregister fail-loud contract (prune.ts: ok on known id, 404 +
// {error} on unknown).

import { afterAll, afterEach, beforeAll, beforeEach, expect, test } from "bun:test"
import { cmdAgentDown } from "./commands-agent-down"
import { trapExit } from "./identity-test-harness"

let server: ReturnType<typeof Bun.serve>
let origServer: string | undefined
let unregisterIds: string[] = []
const KNOWN = new Set(["known-agent"])

beforeAll(() => {
  origServer = process.env.PARLAY_SERVER
  server = Bun.serve({
    port: 0,
    async fetch(req) {
      const url = new URL(req.url)
      if (url.pathname === "/api/chat/unregister" && req.method === "POST") {
        const body = await req.json()
        const id = String(body.id ?? "").trim()
        unregisterIds.push(id)
        if (!KNOWN.has(id)) return Response.json({ error: `unknown channel: ${id}` }, { status: 404 })
        KNOWN.delete(id)
        return Response.json({ ok: true, id })
      }
      return new Response("not found", { status: 404 })
    },
  })
  process.env.PARLAY_SERVER = `http://localhost:${server.port}`
})

afterAll(() => {
  server.stop(true)
  if (origServer === undefined) delete process.env.PARLAY_SERVER
  else process.env.PARLAY_SERVER = origServer
})

let exitTrap: { codes: number[] }
beforeEach(() => {
  exitTrap = trapExit()
  unregisterIds = []
})
afterEach(() => {
  ;(process as unknown as { exit: typeof process.exit }).exit = process.exit
})

test("deregisters a live agent id and prints confirmation", async () => {
  KNOWN.add("known-agent")
  const logs: string[] = []
  const origLog = console.log
  console.log = (...a: unknown[]) => logs.push(a.join(" "))
  try {
    await cmdAgentDown(["known-agent"])
  } finally {
    console.log = origLog
  }
  expect(unregisterIds).toEqual(["known-agent"])
  expect(logs.some(l => l.includes("known-agent") && l.includes("deregistered"))).toBe(true)
  expect(exitTrap.codes).toEqual([])
})

test("fails loud (non-zero exit) on an unknown id — never a silent success", async () => {
  await expect(cmdAgentDown(["nonexistent-agent"])).rejects.toThrow()
  expect(unregisterIds).toEqual(["nonexistent-agent"])
  expect(exitTrap.codes).toEqual([1])
})

test("requires an agent id (usage error, exit 2)", async () => {
  await expect(cmdAgentDown([])).rejects.toThrow()
  expect(exitTrap.codes).toEqual([2])
})
