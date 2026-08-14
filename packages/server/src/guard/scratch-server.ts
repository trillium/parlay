// Spawns a REAL server process for this folder's integration tests, on a
// scratch port with HOME, PARLAY_DATA_DIR and PAI_DIR redirected into a temp
// dir — it never touches ~/exchange, ~/.parlay, or the captain's live
// instance on :31337. Not a .test.ts, so it is never collected as a suite.

import { mkdtempSync, rmSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"

export const EVIL = "https://evil.example.com"

export interface ScratchServer {
  /** http://127.0.0.1:<port> — what a request is sent to. */
  base: string
  /** http://localhost:<port> — the panel's own origin. */
  origin: string
  stop(): void
}

/**
 * Starts the server and resolves once it answers. Each caller gets its own
 * process on its own random 45xxx port, so test files stay independent.
 */
export async function startScratchServer(): Promise<ScratchServer> {
  const port = 45000 + Math.floor(Math.random() * 900)
  const dir = mkdtempSync(join(tmpdir(), "parlay-guard-"))
  const base = `http://127.0.0.1:${port}`

  const proc = Bun.spawn(["bun", join(import.meta.dir, "..", "index.ts")], {
    env: {
      ...process.env,
      HOME: dir,
      PARLAY_PORT: String(port),
      PARLAY_DATA_DIR: join(dir, "exchange"),
      PAI_DIR: join(dir, "pai"),
      PARLAY_STATE_HOME: join(dir, "state"),
      PARLAY_ALLOWED_ORIGINS: "",
      // /api/chat/eval relays to the compiled Go engine, whose coded default
      // is 127.0.0.1:4343 — the captain's LIVE eval engine. Point it at a
      // dead scratch port so an accepted (same-origin / no-Origin) eval
      // answers 502 "engine unreachable" instead of reaching the real one.
      // A 502 is exactly as good a proof as a 200 here: the request got past
      // the guard and into the handler, which is what these tests assert.
      PARLAY_EVAL_ENGINE_URL: `http://127.0.0.1:${port + 1}`,
    },
    stdout: "pipe",
    stderr: "pipe",
  })

  const stop = () => {
    proc.kill()
    rmSync(dir, { recursive: true, force: true })
  }

  for (let i = 0; i < 100; i++) {
    try {
      await fetch(`${base}/api/chat/history`)
      return { base, origin: `http://localhost:${port}`, stop }
    } catch { await Bun.sleep(100) }
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
