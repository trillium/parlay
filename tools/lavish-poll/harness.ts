// Shared test harness for the lavish-poll integration tests.
//
// The bridge is a top-level script that ends in process.exit(), so it is
// exercised as a subprocess rather than imported. Every run gets a throwaway
// PARLAY_RELAY_RUNTIME and points at ephemeral ports, so no test can reach the
// captain's live Parlay on :4242 or the real cursor files under $TMPDIR/parlay.

import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

export const BRIDGE = join(import.meta.dir, "index.ts")

/** A port with nothing listening, so a fetch fails fast rather than hanging. */
export const DEAD = "http://127.0.0.1:1"

export interface RunResult {
  code: number
  stdout: string
  stderr: string
  json: any
}

/** Runs the bridge to completion and parses whatever single JSON line it emits. */
export async function runBridge(opts: {
  args: string[]
  parlayUrl?: string
  nativeUrl?: string
  runtime?: string
  /** Extra env, for shrinking a guard budget so a test need not wait it out. */
  env?: Record<string, string>
}): Promise<RunResult> {
  const runtime = opts.runtime ?? mkdtempSync(join(tmpdir(), "lavish-poll-test-"))
  const proc = Bun.spawn(["bun", BRIDGE, "agent-test", opts.parlayUrl ?? DEAD, ...opts.args], {
    env: {
      ...process.env,
      LAVISH_URL: opts.nativeUrl ?? DEAD,
      PARLAY_RELAY_RUNTIME: runtime,
      ...opts.env,
    },
    stdout: "pipe",
    stderr: "pipe",
  })
  const [stdout, stderr, code] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ])
  let json: any = null
  try {
    json = JSON.parse(stdout.trim().split("\n").filter(Boolean).pop() ?? "")
  } catch {}
  return { code, stdout, stderr, json }
}

/** A server that accepts the connection and then never answers. */
export function stalling() {
  return Bun.serve({ port: 0, fetch: () => new Promise<Response>(() => {}) })
}

/** Serves `first` on the initial `after=""` poll, then stalls forever. */
export function onceThenStall(first: unknown) {
  const seen: string[] = []
  const server = Bun.serve({
    port: 0,
    fetch(req) {
      const after = new URL(req.url).searchParams.get("after") ?? ""
      seen.push(after)
      if (after === "") return Response.json(first as any)
      return new Promise<Response>(() => {})
    },
  })
  return { server, seen, url: `http://127.0.0.1:${server.port}` }
}
