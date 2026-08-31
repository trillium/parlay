// Spawns a REAL server process for this folder's integration tests, on a
// scratch port with HOME, PARLAY_DATA_DIR, PAI_DIR and PARLAY_STATE_HOME
// redirected into a temp dir — it never touches ~/exchange, ~/.parlay, or the
// captain's live instance on :31337. Not a .test.ts, so it is never collected
// as a suite.

import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"

export const EVIL = "https://evil.example.com"

export interface ScratchServer {
  /** http://127.0.0.1:<port> — what a request is sent to. */
  base: string
  /**
   * http://127.0.0.1:<port> — the panel's real shape, served by the server it
   * calls. It satisfies BOTH of originAllowed's accept branches at once (its
   * host:port equals `base`'s, AND `127.` is a private-v4 literal), so it
   * proves neither branch in isolation. Use `tunnelOrigin` for the same-host
   * comparison and `loopbackOrigin` for the hostname branch.
   */
  panelOrigin: string
  /**
   * http://localhost:<port> — a loopback HOSTNAME that does NOT match `base`'s
   * host, so only originAllowed's isLocalHostname branch can accept it.
   */
  loopbackOrigin: string
  /**
   * http://panel.tunnel.test:<port>, to be sent with `Host: tunnelHost`. Its
   * hostname is not loopback, not private-LAN, not .local and not in
   * PARLAY_ALLOWED_ORIGINS (which this instance sets empty), so the same-host
   * comparison is the ONLY thing that can accept it — the deployment shape
   * being a panel behind a Host-forwarding tunnel or reverse proxy. Send it
   * with `tunnelForeignHost` instead and it must be refused. No DNS is
   * involved: Host and Origin are just headers on a request still dialled at
   * `base`.
   */
  tunnelOrigin: string
  tunnelHost: string
  tunnelForeignHost: string
  /** PARLAY_DATA_DIR — every file this instance persists lands here. */
  dataDir: string
  /** PARLAY_STATE_HOME — where debug.log and other state files land. */
  stateDir: string
  stop(): void
}

// Reserves a port by actually binding it, then releasing it. A port taken by
// something else cannot be handed out twice, so this is the difference
// between "probably free" and "was free a moment ago" — a random port in a
// fixed range is neither, and a collision would have silently pointed a test
// at somebody else's server.
function reservePort(): number {
  const srv = Bun.serve({ port: 0, fetch: () => new Response("reserved") })
  const port = srv.port
  srv.stop(true)
  return port
}

// Proves nothing is listening: a connect to a closed port is refused, so
// fetch must REJECT. If it resolves, some other process owns that port and
// the eval tests' "502 engine unreachable" would be a lie.
async function assertNotListening(url: string): Promise<void> {
  try {
    await fetch(url, { signal: AbortSignal.timeout(1000) })
  } catch {
    return
  }
  throw new Error(`${url} is answering — the dead eval-engine port is not dead`)
}

/**
 * Starts the server and resolves once it answers AS ITSELF. Each caller gets
 * its own process on its own reserved port with its own data dir, so test
 * files stay independent.
 */
export async function startScratchServer(): Promise<ScratchServer> {
  const port = reservePort()
  const evalPort = reservePort()
  const dir = mkdtempSync(join(tmpdir(), "parlay-guard-"))
  const dataDir = join(dir, "exchange")
  const base = `http://127.0.0.1:${port}`

  // Identity marker: seeded into this instance's history before it boots, so
  // the readiness probe can tell OUR server from anything else that might be
  // answering on this port. Without it, a squatter's 200 reads as ready and
  // every assertion after it is about the wrong process.
  const marker = `scratch-${port}-${crypto.randomUUID()}`
  mkdirSync(dataDir, { recursive: true })
  writeFileSync(
    join(dataDir, "chat-history.jsonl"),
    JSON.stringify({ id: marker, role: "agent", text: marker, ts: new Date().toISOString() }) + "\n",
    "utf8",
  )

  await assertNotListening(`http://127.0.0.1:${evalPort}/`)

  const proc = Bun.spawn(["bun", join(import.meta.dir, "..", "index.ts")], {
    env: {
      ...process.env,
      HOME: dir,
      PARLAY_PORT: String(port),
      PARLAY_DATA_DIR: dataDir,
      PAI_DIR: join(dir, "pai"),
      PARLAY_STATE_HOME: join(dir, "state"),
      PARLAY_ALLOWED_ORIGINS: "",
      // /api/chat/eval relays to the compiled Go engine, whose coded default
      // is 127.0.0.1:4343 — the captain's LIVE eval engine. Point it at a
      // reserved-then-released port, verified above to refuse connections, so
      // an accepted (same-origin / no-Origin) eval answers 502 "engine
      // unreachable" instead of reaching the real one. A 502 is exactly as
      // good a proof as a 200 here: the request got past the guard and into
      // the handler, which is what these tests assert.
      PARLAY_EVAL_ENGINE_URL: `http://127.0.0.1:${evalPort}`,
    },
    stdout: "pipe",
    stderr: "pipe",
  })

  const stop = () => {
    proc.kill()
    rmSync(dir, { recursive: true, force: true })
  }

  for (let i = 0; i < 100; i++) {
    if (proc.exitCode !== null) {
      const err = await new Response(proc.stderr).text()
      stop()
      throw new Error(`scratch parlay server exited ${proc.exitCode} before answering:\n${err}`)
    }
    try {
      const body = await (await fetch(`${base}/api/chat/history`)).text()
      if (!body.includes(marker)) {
        stop()
        throw new Error(`${base} is answering, but it is not our server — no identity marker in /api/chat/history`)
      }
      return {
        base,
        panelOrigin: base,
        loopbackOrigin: `http://localhost:${port}`,
        tunnelOrigin: `http://panel.tunnel.test:${port}`,
        tunnelHost: `panel.tunnel.test:${port}`,
        tunnelForeignHost: `other.tunnel.test:${port}`,
        dataDir,
        stateDir: join(dir, "state"),
        stop,
      }
    } catch (e) {
      if (e instanceof Error && e.message.includes("not our server")) throw e
      await Bun.sleep(100)
    }
  }
  stop()
  throw new Error("scratch parlay server never came up")
}

/** A 1x1 transparent GIF — real bytes, so the upload handler's image check passes. */
export function pixelGif(): Uint8Array {
  return Uint8Array.from(
    atob("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"),
    c => c.charCodeAt(0),
  )
}

/** A multipart upload body, the shape the panel's FormData produces. */
export function uploadForm(): FormData {
  const form = new FormData()
  form.set("file", new File([pixelGif()], "pixel.gif", { type: "image/gif" }))
  return form
}
