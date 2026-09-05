import { describe, test, expect, beforeAll, afterAll } from "bun:test"
import { serve } from "bun"
import { mkdtempSync, rmSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { POLL_WAIT_MS, SSE_KEEPALIVE_MS } from "./timeouts"

// task-2nwlw — the :4242 wedge, reproduced against the REAL server.
//
// `Bun.serve()` applies a 10s `idleTimeout` by default and destroys any quieter
// connection. Because the client already holds the status line, it observes
// HTTP 200 with an EMPTY body — which is exactly how the production wedge
// presented: TCP still accepting, every long-lived route returning 0 bytes,
// process at 0% CPU.
//
// This boots src/index.ts as its own process and drives the two routes that
// deliberately outlive that default:
//
//   GET /api/chat/poll    parks a waiter POLL_WAIT_MS (30s) before answering
//   GET /api/chat/events  SSE whose first keepalive is SSE_KEEPALIVE_MS (25s) away
//
// Both assertions are on bytes the server actually emitted, never on elapsed
// time (bun startup jitter is larger than most intervals worth asserting on).
// Un-fix index.ts and both fail: the poll yields an empty body instead of
// {"timeout":true}, and the SSE stream is torn down before `: ka` can ever be
// written even once.
//
// The two drives share one boot and run concurrently, so the file costs about
// one POLL_WAIT_MS rather than the sum of both holds.

const SERVER = join(import.meta.dir, "index.ts")

let proc: ReturnType<typeof Bun.spawn> | undefined
let sandbox = ""
let base = ""

/** An ephemeral port the OS just confirmed free. Never 4242 — that is the captain's live server. */
function freePort(): number {
  const probe = serve({ port: 0, fetch: () => new Response("") })
  const { port } = probe
  probe.stop(true)
  return port
}

async function waitForHealth(url: string, deadlineMs: number): Promise<boolean> {
  const giveUpAt = Date.now() + deadlineMs
  while (Date.now() < giveUpAt) {
    try {
      const res = await fetch(`${url}/health`)
      if (res.ok && (await res.json() as { ok?: boolean }).ok) return true
    } catch { /* not listening yet */ }
    await Bun.sleep(100)
  }
  return false
}

beforeAll(async () => {
  sandbox = mkdtempSync(join(tmpdir(), "parlay-holds-"))
  const port = freePort()
  base = `http://127.0.0.1:${port}`

  // The four redirects a test instance needs (AGENTS.md): PARLAY_DATA_DIR only
  // covers what resolves through paths.ts, so HOME, PARLAY_STATE_HOME and
  // PAI_DIR have to move too or this writes into the captain's ~/.parlay and
  // the tailers replay live agent turns. PARLAY_HUB_URL defaults to :4242 —
  // pinned here so no push can reach the production port.
  proc = Bun.spawn(["bun", "run", SERVER], {
    cwd: join(import.meta.dir, ".."),
    env: {
      ...process.env,
      HOME:              sandbox,
      PARLAY_DATA_DIR:   join(sandbox, "data"),
      PARLAY_STATE_HOME: join(sandbox, "state"),
      PAI_DIR:           join(sandbox, "pai"),
      PARLAY_PORT:       String(port),
      PARLAY_HUB_URL:    base,
    },
    stdout: "pipe",
    stderr: "pipe",
  })

  expect(await waitForHealth(base, 60_000)).toBe(true)
}, 90_000)

afterAll(async () => {
  proc?.kill()
  await proc?.exited
  if (sandbox) rmSync(sandbox, { recursive: true, force: true })
})

describe("the routes this server holds open outlive Bun's default idleTimeout", () => {
  test("a parked long-poll answers with a body, and an SSE stream lives to send a keepalive", async () => {
    // Bun's own client throws when the runtime destroys the connection; curl
    // sees the same event as 200 with 0 bytes. Either way: no body.
    const poll = fetch(`${base}/api/chat/poll?channel=${crypto.randomUUID()}`)
      .then(r => r.text())
      .catch(() => "<connection destroyed>")

    // Read the SSE stream until the first keepalive comment appears. The
    // connect burst (connected/history/agents/...) arrives immediately and
    // proves nothing — only a frame written SSE_KEEPALIVE_MS later does.
    const sse = (async () => {
      const res = await fetch(`${base}/api/chat/events`)
      const reader = res.body!.getReader()
      const dec = new TextDecoder()
      let seen = ""
      try {
        // Bounded by the poll hold, which is the longer of the two.
        const giveUpAt = Date.now() + POLL_WAIT_MS + 5_000
        while (Date.now() < giveUpAt) {
          const { done, value } = await reader.read()
          if (done) break                       // destroyed: stream ended early
          seen += dec.decode(value, { stream: true })
          if (seen.includes(": ka")) break
        }
      } catch { /* destroyed mid-read */ }
      finally { await reader.cancel().catch(() => {}) }
      return seen
    })()

    const [pollBody, sseText] = await Promise.all([poll, sse])

    // The long-poll survived to write its timeout envelope.
    expect(pollBody).not.toBe("")
    expect(pollBody).not.toBe("<connection destroyed>")
    expect(JSON.parse(pollBody)).toMatchObject({ timeout: true })

    // The SSE keepalive fired at least once. Under Bun's default this line is
    // unreachable by construction: SSE_KEEPALIVE_MS is past the cutoff.
    expect(SSE_KEEPALIVE_MS).toBeGreaterThan(10_000)   // i.e. past Bun's default
    expect(sseText).toContain(": ka")
  }, 90_000)
})
