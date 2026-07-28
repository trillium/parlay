// Unit tests for `parlay listen` — the one-call register+announce+monitor
// enrollment. Exercises runListen with injected fakes (postJSON, runMonitor,
// die) so no network/process is touched, mirroring the MonitorDeps pattern.

import { test, expect } from "bun:test"
import { runListen, type ListenDeps } from "./listen"
import { parseArgs } from "./args"
import { helpWanted } from "./help"

function makeDeps(overrides: Partial<ListenDeps> = {}) {
  const calls: { path: string; body: unknown }[] = []
  const monitorCalls: { args: string[] }[] = []
  const deps: ListenDeps = {
    server: "http://test-server",
    exitUsage: 2,
    die: ((msg: string) => { throw new Error(msg) }) as ListenDeps["die"],
    helpWanted,
    parseArgs,
    postJSON: async (path: string, body: unknown) => { calls.push({ path, body }); return { ok: true } },
    runMonitor: async (args: string[]) => { monitorCalls.push({ args }) },
    ...overrides,
  }
  return { deps, calls, monitorCalls }
}

test("--agent is required", async () => {
  const { deps } = makeDeps()
  await expect(runListen([], deps)).rejects.toThrow(/--agent <id> is required/)
})

test("registers, announces, then hands off to the monitor loop in order", async () => {
  const { deps, calls, monitorCalls } = makeDeps()
  await runListen(["--agent", "brain-dev"], deps)

  expect(calls.length).toBe(2)
  expect(calls[0].path).toBe("/api/chat/register-agent")
  expect(calls[0].body).toMatchObject({ id: "brain-dev", name: "brain-dev" })
  expect(calls[1].path).toBe("/api/chat/reply")
  expect(calls[1].body).toMatchObject({ agent: "brain-dev" })
  expect((calls[1].body as { text: string }).text).toMatch(/listening/)

  expect(monitorCalls.length).toBe(1)
  expect(monitorCalls[0].args).toEqual(["--agent", "brain-dev"])
})

test("--name overrides the default (agent id) display name", async () => {
  const { deps, calls } = makeDeps()
  await runListen(["--agent", "brain-dev", "--name", "Brain Dev"], deps)
  expect(calls[0].body).toMatchObject({ id: "brain-dev", name: "Brain Dev" })
})

test("same agent id always derives the same deterministic color", async () => {
  const { deps: deps1, calls: calls1 } = makeDeps()
  const { deps: deps2, calls: calls2 } = makeDeps()
  await runListen(["--agent", "brain-dev"], deps1)
  await runListen(["--agent", "brain-dev"], deps2)
  expect((calls1[0].body as { color: string }).color).toBe((calls2[0].body as { color: string }).color)
  expect((calls1[0].body as { color: string }).color).toMatch(/^#[0-9a-f]{6}$/)
})

test("--caps forwards parsed JSON on the registry call", async () => {
  const { deps, calls } = makeDeps()
  await runListen(["--agent", "brain-dev", "--caps", '{"tools":["bash"]}'], deps)
  expect(calls[0].body).toMatchObject({ caps: { tools: ["bash"] } })
})

test("invalid --caps JSON dies with a usage error, before any network call", async () => {
  const { deps, calls } = makeDeps()
  await expect(runListen(["--agent", "brain-dev", "--caps", "{not json"], deps)).rejects.toThrow(/--caps must be valid JSON/)
  expect(calls.length).toBe(0)
})

test("--legacy-poll is forwarded to the monitor loop", async () => {
  const { deps, monitorCalls } = makeDeps()
  await runListen(["--agent", "brain-dev", "--legacy-poll"], deps)
  expect(monitorCalls[0].args).toEqual(["--agent", "brain-dev", "--legacy-poll"])
})

test("re-running is idempotent — same calls, no accumulation across runs", async () => {
  const { deps, calls, monitorCalls } = makeDeps()
  await runListen(["--agent", "brain-dev"], deps)
  await runListen(["--agent", "brain-dev"], deps)
  expect(calls.length).toBe(4) // 2 calls × 2 runs, not growing per-run state
  expect(monitorCalls.length).toBe(2)
})

test("--help prints usage and does not touch the network or monitor", async () => {
  const { deps, calls, monitorCalls } = makeDeps()
  await runListen(["--help"], deps)
  expect(calls.length).toBe(0)
  expect(monitorCalls.length).toBe(0)
})
