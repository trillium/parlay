// Port probing + the Parlay isolation-env contract, in one place.
//
// The whole point of split-testing is provable isolation: every sandbox picks a
// FREE high port (never a reserved prod port) and asserts, after boot, that the
// process actually honored the env override. If it didn't, sandbox `up` fails
// loudly rather than silently sharing a prod store — that is a safety property,
// not a nicety.

import { connect, createServer } from "net"

// Ports the prod stack and the pulse-next proxy own. A sandbox must never bind
// any of these — doing so would collide with the live system this tool exists to
// protect. `two-door` may TARGET some of them (they are external front doors it
// probes), but it never BINDS them.
export const RESERVED_PORTS: readonly number[] = [31337, 31338, 31339, 4242, 4343] as const

// Sandbox components pick from this window upward. High enough to avoid the
// reserved set and anything a normal dev stack grabs.
export const SANDBOX_PORT_BASE = 42000
export const SANDBOX_PORT_MAX = 42999

/** True if something is already accepting connections on host:port. */
function connectSucceeds(port: number, host: string): Promise<boolean> {
  return new Promise((resolve) => {
    const sock = connect({ port, host })
    const done = (v: boolean) => {
      sock.destroy()
      resolve(v)
    }
    sock.once("connect", () => done(true))
    sock.once("error", () => done(false))
    sock.setTimeout(400, () => done(false))
  })
}

/** True if a fresh bind on host:port fails (something holds it, or it's blocked). */
function bindFails(port: number, host: string): Promise<boolean> {
  return new Promise((resolve) => {
    const srv = createServer()
    srv.once("error", () => resolve(true))
    srv.once("listening", () => srv.close(() => resolve(false)))
    srv.listen(port, host)
  })
}

/**
 * True if `port` is NOT safe to hand out. A port is in use if EITHER:
 *   - a connect probe succeeds (something is accepting), OR
 *   - a fresh bind fails.
 * The connect probe is essential: bun's `serve()` binds with SO_REUSEADDR/
 * reusePort, so a bind test ALONE reports a bun-held port as free — which would
 * hand the same port to two sandboxes (observed: two-stack collided on 42000).
 * The bind test still catches ports nothing is accepting on yet but which are
 * otherwise unbindable. Either signal → in use.
 */
export async function portInUse(port: number, host = "127.0.0.1"): Promise<boolean> {
  if (await connectSucceeds(port, host)) return true
  return bindFails(port, host)
}

/**
 * Find the first free TCP port at/above `base`, refusing to ever return a
 * reserved prod port. Throws if the whole window is exhausted — a caller must
 * never fall back to a reserved port silently.
 */
export async function findFreePort(base = SANDBOX_PORT_BASE, max = SANDBOX_PORT_MAX): Promise<number> {
  for (let p = base; p <= max; p++) {
    if (RESERVED_PORTS.includes(p)) continue
    if (!(await portInUse(p))) return p
  }
  throw new Error(`no free port in [${base}, ${max}] — all in use`)
}

/**
 * Assert a TCP listener IS up on host:port within `timeoutMs`, polling with a
 * connect probe. Used after boot to prove the process bound the port we told it
 * to via env. Returns true on success; false on timeout.
 */
export function waitForListen(port: number, host = "127.0.0.1", timeoutMs = 8000): Promise<boolean> {
  const deadline = Date.now() + timeoutMs
  const attempt = (): Promise<boolean> =>
    new Promise((resolve) => {
      const sock = connect({ port, host })
      const done = (ok: boolean) => {
        sock.destroy()
        resolve(ok)
      }
      sock.once("connect", () => done(true))
      sock.once("error", () => done(false))
      sock.setTimeout(500, () => done(false))
    })
  return (async () => {
    for (;;) {
      if (await attempt()) return true
      if (Date.now() >= deadline) return false
      await new Promise((r) => setTimeout(r, 150))
    }
  })()
}

/** Guard: refuse to bind a reserved prod port. Throws with a clear reason. */
export function assertNotReserved(port: number, what: string): void {
  if (RESERVED_PORTS.includes(port)) {
    throw new Error(`refusing to bind reserved prod port ${port} for ${what} — sandbox must use a free high port`)
  }
}

/**
 * The env override contract, one object. Every field maps to a real env var the
 * server/relay/eval-engine read (verified against packages/{server,cli,eval-engine}
 * and tools/relay). Building the env explicitly — rather than mutating
 * process.env — is what lets the boot step assert isolation end to end.
 */
export interface ParlayEnv {
  /** server listen port (server reads PARLAY_PORT, default 4242) */
  PARLAY_PORT: string
  /** server history/spool dir (server reads PARLAY_DATA_DIR, default ~/exchange) */
  PARLAY_DATA_DIR: string
  /** CLI/relay target base URL (default http://localhost:4242 for CLI, :31337 for relay) */
  PARLAY_SERVER: string
  /** relay spool + control-socket dir (resolved by CLI/deploy, passed to relay as --runtime-dir) */
  PARLAY_RELAY_RUNTIME: string
  /** eval-engine client URL (server + CLI read PARLAY_EVAL_ENGINE_URL, default http://127.0.0.1:4343) */
  PARLAY_EVAL_ENGINE_URL: string
  /** eval-engine listen addr (engine reads PARLAY_EVAL_ADDR, default 127.0.0.1:4343) */
  PARLAY_EVAL_ADDR: string
  /** per-agent home for identity/scratchpad (default ~/.parlay/agents) */
  PARLAY_AGENT_HOME: string
  /**
   * PAI root the server keys several persistence paths off (agent registry
   * parlay-agents.json, tts cache/reports, tool/hook tailers). NOT covered by
   * PARLAY_DATA_DIR — see README "Isolation gaps". We redirect it to a
   * sandbox-local dir so a sandbox never rehydrates or overwrites the prod
   * agent registry. This is a sandbox-level env override, not a prod-path patch.
   */
  PAI_DIR: string
}

/**
 * Build a fully-isolated Parlay env for a sandbox named `name`, rooted under
 * `cacheDir`. Ports must already be chosen (and proven free) by the caller.
 */
export function buildSandboxEnv(opts: {
  serverPort: number
  evalPort: number | null
  dataDir: string
  runtimeDir: string
  agentHome: string
  paiDir: string
  host?: string
}): ParlayEnv {
  const host = opts.host ?? "127.0.0.1"
  const evalUrl = opts.evalPort !== null ? `http://${host}:${opts.evalPort}` : ""
  return {
    PARLAY_PORT: String(opts.serverPort),
    PARLAY_DATA_DIR: opts.dataDir,
    PARLAY_SERVER: `http://${host}:${opts.serverPort}`,
    PARLAY_RELAY_RUNTIME: opts.runtimeDir,
    PARLAY_EVAL_ENGINE_URL: evalUrl,
    PARLAY_EVAL_ADDR: opts.evalPort !== null ? `${host}:${opts.evalPort}` : "",
    PARLAY_AGENT_HOME: opts.agentHome,
    PAI_DIR: opts.paiDir,
  }
}
