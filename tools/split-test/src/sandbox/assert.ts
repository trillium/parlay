// The isolation proof step. assertIsolation throws (after tearing the sandbox
// back down) if ANY env override was not honored end to end, or if prod was
// disturbed. This is the whole point of `sandbox up` — proving the PARLAY_* /
// PAI_DIR isolation surface actually works.

import { existsSync } from "fs"
import { join } from "path"
import { homedir } from "os"
import { waitForListen } from "../env"
import type { SandboxManifest } from "../manifest"
import { log, snapshotProdPids, sameSet } from "./common"

/** Poll for a file to appear, up to timeoutMs. */
async function pollExists(path: string, timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs
  for (;;) {
    if (existsSync(path)) return true
    if (Date.now() >= deadline) return false
    await new Promise((r) => setTimeout(r, 150))
  }
}

/** Ask the relay (over its unix control socket) what runtime dir it resolved. */
async function queryRelayRuntime(sockPath: string): Promise<string | null> {
  if (!existsSync(sockPath)) return null
  try {
    // Bun's fetch supports unix sockets via the `unix` option.
    const res = await fetch("http://localhost/agents", { unix: sockPath })
    if (!res.ok) return null
    const body = (await res.json()) as { runtime?: string }
    return body.runtime ?? null
  } catch {
    return null
  }
}

/** Query a running server's agent-registry size. Returns null on failure. */
async function fetchAgentCount(serverUrl: string): Promise<number | null> {
  try {
    const res = await fetch(`${serverUrl}/api/chat/agents`, { signal: AbortSignal.timeout(3000) })
    if (!res.ok) return null
    const body = (await res.json()) as unknown
    return Array.isArray(body) ? body.length : null
  } catch {
    return null
  }
}

/**
 * Assert every isolation property, collecting all failures before deciding. On
 * any failure, tear the sandbox down (via `teardown`) and throw. Checks:
 *   1. server listening on the chosen PARLAY_PORT
 *   2. data dir is ours, never prod ~/exchange (PARLAY_DATA_DIR)
 *  2b. sandbox agent registry empty — proves PAI_DIR redirect took
 *   3. relay.sock in our runtime dir + relay reports our runtime (--runtime-dir)
 *   4. eval-engine (if any) listening on chosen PARLAY_EVAL_ADDR
 *   5. prod relay + eval-engine PIDs unchanged
 */
export async function assertIsolation(
  m: SandboxManifest,
  prodBefore: { relay: number[]; engine: number[] },
  teardown: (m: SandboxManifest) => void,
): Promise<void> {
  const fails: string[] = []
  const server = m.components.find((c) => c.kind === "server")!
  const relay = m.components.find((c) => c.kind === "relay")!
  const engine = m.components.find((c) => c.kind === "eval-engine")

  // 1. server listening on chosen port
  const serverPort = server.port!
  if (!(await waitForListen(serverPort, undefined, 10_000))) {
    fails.push(`server NOT listening on chosen port ${serverPort} — PARLAY_PORT override not respected (see ${server.logFile})`)
  }

  // 2. data dir honored — the sandbox data dir must exist and be ours, and the
  //    default ~/exchange must NOT have been the target.
  const expectedData = m.env.PARLAY_DATA_DIR
  if (!existsSync(expectedData)) fails.push(`data dir ${expectedData} missing — server did not create it`)
  if (expectedData === join(homedir(), "exchange")) {
    fails.push(`data dir resolved to prod ~/exchange — PARLAY_DATA_DIR override not respected`)
  }

  // 2b. PAI_DIR (agent registry) isolation — the sandbox server must NOT have
  //     rehydrated the prod agent registry. If PAI_DIR were ignored, the server
  //     loads prod's parlay-agents.json (~20+ agents) at boot. A fresh sandbox
  //     starts empty, so a non-empty registry on first boot means the leak.
  const sandboxAgents = await fetchAgentCount(m.env.PARLAY_SERVER)
  if (sandboxAgents === null) {
    fails.push(`could not query sandbox server /api/chat/agents to verify PAI_DIR isolation`)
  } else if (sandboxAgents > 0) {
    fails.push(`sandbox server rehydrated ${sandboxAgents} agent(s) at boot — PAI_DIR override not respected (prod registry leaked in). Expected 0 in a fresh sandbox.`)
  }

  // 3. relay runtime dir honored — relay.sock must appear in OUR runtime dir.
  const sock = join(m.env.PARLAY_RELAY_RUNTIME, "relay.sock")
  if (!(await pollExists(sock, 8_000))) {
    fails.push(`relay control socket ${sock} not created — PARLAY_RELAY_RUNTIME/--runtime-dir override not respected (see ${relay.logFile})`)
  }
  const reported = await queryRelayRuntime(sock)
  if (reported !== null && reported !== m.env.PARLAY_RELAY_RUNTIME) {
    fails.push(`relay reports runtime "${reported}" but we requested "${m.env.PARLAY_RELAY_RUNTIME}" — override not respected`)
  }

  // 4. eval-engine listening
  if (engine && !(await waitForListen(engine.port!, undefined, 10_000))) {
    fails.push(`eval-engine NOT listening on chosen port ${engine.port} — PARLAY_EVAL_ADDR override not respected (see ${engine.logFile})`)
  }

  // 5. prod PIDs unchanged
  const prodAfter = snapshotProdPids()
  if (!sameSet(prodBefore.relay, prodAfter.relay)) {
    fails.push(`prod relay PIDs changed: before=[${prodBefore.relay}] after=[${prodAfter.relay}] — SANDBOX DISTURBED PROD`)
  }
  // Our own sandbox eval-engine ALSO matches "parlay-eval-engine", so the engine
  // set legitimately grows by our pid. Only flag a REMOVED pre-existing prod pid.
  const prodEngineLost = prodBefore.engine.filter((p) => !prodAfter.engine.includes(p))
  if (prodEngineLost.length > 0) {
    fails.push(`prod eval-engine PID(s) disappeared: [${prodEngineLost}] — SANDBOX DISTURBED PROD`)
  }

  if (fails.length > 0) {
    log(`ISOLATION FAILED — tearing down sandbox "${m.name}"`)
    teardown(m)
    throw new Error(`sandbox up failed isolation assertions:\n  - ${fails.join("\n  - ")}`)
  }
  log(`isolation verified: port=${serverPort} data=${expectedData} runtime=${m.env.PARLAY_RELAY_RUNTIME}${engine ? ` eval=${engine.port}` : ""}; prod relay pid(s) [${prodBefore.relay}] unchanged`)
}
