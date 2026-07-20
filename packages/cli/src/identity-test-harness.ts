// Shared test harness for the store-mutating identity verbs. Drives cmdIdentity
// end-to-end against a tmp PARLAY_AGENT_HOME and a throwaway Bun.serve that
// records /api/chat/register-agent bodies. NOT a test file itself — imported by
// the per-capability *.test.ts files so they share one server + cleanup path.

import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"

export type Harness = {
  homes: string[]
  registerBodies: Array<Record<string, unknown>>
  server: ReturnType<typeof Bun.serve>
  origHome: string | undefined
  origServer: string | undefined
  origExit: typeof process.exit
}

// Start the recording server and snapshot env. Call from beforeAll.
export function startHarness(): Harness {
  const h: Harness = {
    homes: [],
    registerBodies: [],
    origHome: process.env.PARLAY_AGENT_HOME,
    origServer: process.env.PARLAY_SERVER,
    origExit: process.exit,
    server: undefined as unknown as ReturnType<typeof Bun.serve>,
  }
  h.server = Bun.serve({
    port: 0,
    async fetch(req) {
      const url = new URL(req.url)
      if (url.pathname === "/api/chat/register-agent" && req.method === "POST") {
        h.registerBodies.push(await req.json())
        return Response.json({ ok: true })
      }
      return new Response("not found", { status: 404 })
    },
  })
  process.env.PARLAY_SERVER = `http://localhost:${h.server.port}`
  return h
}

// Stop the server and restore PARLAY_SERVER. Call from afterAll.
export function stopHarness(h: Harness): void {
  h.server.stop(true)
  if (h.origServer === undefined) delete process.env.PARLAY_SERVER
  else process.env.PARLAY_SERVER = h.origServer
}

// Restore exit + env + clean tmp homes. Call from afterEach.
export function resetHarness(h: Harness): void {
  ;(process as unknown as { exit: typeof process.exit }).exit = h.origExit
  if (h.origHome === undefined) delete process.env.PARLAY_AGENT_HOME
  else process.env.PARLAY_AGENT_HOME = h.origHome
  h.registerBodies.length = 0
  for (const d of h.homes.splice(0)) rmSync(d, { recursive: true, force: true })
}

// Create a fresh tmp agent-home and point PARLAY_AGENT_HOME at it.
export function freshHome(h: Harness): string {
  const dir = mkdtempSync(join(tmpdir(), "parlay-agents-"))
  h.homes.push(dir)
  process.env.PARLAY_AGENT_HOME = dir
  return dir
}

// Seed a minimal agent store (identity.md frontmatter + context.json) on disk.
export function seedAgent(
  home: string,
  id: string,
  opts: { ephemeral?: boolean; name?: string; color?: string; reincarnation?: boolean } = {},
): string {
  const dir = join(home, id)
  mkdirSync(dir, { recursive: true })
  const name = opts.name ?? id
  const color = opts.color ?? "#123456"
  const fmLines = [`id: ${id}`, `name: ${name}`, `color: "${color}"`, `cwd: /tmp/${id}`]
  if (opts.ephemeral) fmLines.push("ephemeral: true")
  writeFileSync(join(dir, "identity.md"), `---\n${fmLines.join("\n")}\n---\n# Identity — ${id}\n\n- a durable fact\n`)
  writeFileSync(join(dir, "context.json"), JSON.stringify({ id, name, color }, null, 2) + "\n")
  if (opts.reincarnation) writeFileSync(join(dir, "reincarnations.log"), `{"ts":"old","agent":"${id}"}\n`)
  return dir
}

// Install a process.exit that throws so a die() path is assertable instead of
// tearing down the runner. Returns the collected exit codes.
export function trapExit(): { codes: number[] } {
  const codes: number[] = []
  ;(process as unknown as { exit: (c?: number) => never }).exit = ((c?: number) => {
    codes.push(c ?? 0)
    throw new Error(`process.exit(${c ?? 0})`)
  }) as never
  return { codes }
}
