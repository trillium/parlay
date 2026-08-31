// sandbox up / down lifecycle.
//
// up boots server + relay (+ optional eval-engine), records a PID manifest, then
// proves isolation. down kills EXACTLY the recorded PIDs — verifying each still
// matches the command line we started (guards against PID recycling) — never a
// broad pkill, so the prod relay/engine are never touched.

import { existsSync, mkdirSync } from "fs"
import { join, resolve } from "path"
import { buildSandboxEnv, findFreePort, assertNotReserved, RESERVED_PORTS, type ParlayEnv } from "../env"
import {
  ensureSandboxDir,
  logPath,
  writeManifest,
  readManifest,
  removeSandboxDir,
  sandboxExists,
  pidAlive,
  type ComponentRecord,
  type SandboxManifest,
} from "../manifest"
import { log, spawnBg, snapshotProdPids } from "./common"
import { buildRelay, buildEngine, seedRuntimeFiles } from "./build"
import { assertIsolation } from "./assert"

export interface UpOptions {
  name: string
  branchDir: string // parlay checkout to boot from (defaults to this worktree's repo root)
  withEngine: boolean
  host?: string
}

export async function sandboxUp(opts: UpOptions): Promise<SandboxManifest> {
  const host = opts.host ?? "127.0.0.1"
  const branchDir = resolve(opts.branchDir)
  if (sandboxExists(opts.name)) {
    const existing = readManifest(opts.name)
    if (existing && existing.components.some((c) => pidAlive(c.pid))) {
      throw new Error(`sandbox "${opts.name}" already up (live PIDs in manifest) — run 'sandbox down --name ${opts.name}' first`)
    }
    removeSandboxDir(opts.name) // stale manifest, no live pids — clear and continue
  }

  const dir = ensureSandboxDir(opts.name)
  const dataDir = join(dir, "data")
  const runtimeDir = join(dir, "runtime")
  const agentHome = join(dir, "agents")
  const binDir = join(dir, "bin")
  // Sandbox-local PAI root. The server keys the agent registry + tts/observability
  // paths off PAI_DIR, NOT PARLAY_DATA_DIR — redirecting it here is what stops a
  // sandbox from rehydrating/overwriting the prod agent registry (see README).
  const paiDir = join(dir, "pai")
  for (const d of [dataDir, runtimeDir, agentHome, binDir, paiDir]) mkdirSync(d, { recursive: true })

  // Choose free ports — never a reserved prod port.
  const serverPort = await findFreePort()
  assertNotReserved(serverPort, "sandbox server")
  let evalPort: number | null = null
  if (opts.withEngine) {
    evalPort = await findFreePort(serverPort + 1)
    assertNotReserved(evalPort, "sandbox eval-engine")
  }

  const env: ParlayEnv = buildSandboxEnv({ serverPort, evalPort, dataDir, runtimeDir, agentHome, paiDir, host })
  log(`ports: server=${serverPort}${evalPort !== null ? ` eval=${evalPort}` : ""} (reserved avoided: ${RESERVED_PORTS.join(",")})`)

  const prodBefore = snapshotProdPids()

  const childEnv: NodeJS.ProcessEnv = {
    ...process.env,
    PARLAY_PORT: env.PARLAY_PORT,
    PARLAY_DATA_DIR: env.PARLAY_DATA_DIR,
    PARLAY_SERVER: env.PARLAY_SERVER,
    PARLAY_RELAY_RUNTIME: env.PARLAY_RELAY_RUNTIME,
    PARLAY_EVAL_ENGINE_URL: env.PARLAY_EVAL_ENGINE_URL,
    PARLAY_EVAL_ADDR: env.PARLAY_EVAL_ADDR,
    PARLAY_AGENT_HOME: env.PARLAY_AGENT_HOME,
    PAI_DIR: env.PAI_DIR,
  }

  const components: ComponentRecord[] = []
  const now = () => new Date().toISOString()

  // Seed untracked-but-runtime-required working files (parlay-ui.ts/.js) so a
  // fresh worktree can boot the server. Documented, not silently patched over.
  seedRuntimeFiles(branchDir)

  // ── 1. Server (bun) ─────────────────────────────────────────────────────────
  const serverEntry = join(branchDir, "packages", "server", "src", "index.ts")
  if (!existsSync(serverEntry)) throw new Error(`server entry not found at ${serverEntry}`)
  const serverLog = logPath(opts.name, "server")
  const serverPid = spawnBg("bun", [serverEntry], childEnv, branchDir, serverLog)
  components.push({ kind: "server", pid: serverPid, port: serverPort, cmd: `bun ${serverEntry}`, logFile: serverLog, startedAt: now() })
  log(`server pid ${serverPid} → ${env.PARLAY_SERVER}`)

  // ── 2. Eval-engine (optional) ───────────────────────────────────────────────
  if (opts.withEngine && evalPort !== null) {
    const engine = buildEngine(branchDir, binDir)
    const engineLog = logPath(opts.name, "eval-engine")
    // cwd is the sandbox bin dir: the engine's optional beside-binary
    // commands.json lookup goes by os.Executable, not cwd, so any dir works.
    const enginePid = spawnBg(engine.bin, engine.args, childEnv, binDir, engineLog)
    components.push({ kind: "eval-engine", pid: enginePid, port: evalPort, cmd: `${[engine.bin, ...engine.args].join(" ")} (PARLAY_EVAL_ADDR=${env.PARLAY_EVAL_ADDR})`, logFile: engineLog, startedAt: now() })
    log(`eval-engine pid ${enginePid} → ${env.PARLAY_EVAL_ENGINE_URL}`)
  }

  // ── 3. Relay (plain background process, NOT launchd) ─────────────────────────
  const relayBin = buildRelay(branchDir, binDir)
  const relayLog = logPath(opts.name, "relay")
  // Pass the runtime dir EXPLICITLY as --runtime-dir (the relay reads the flag,
  // not PARLAY_RELAY_RUNTIME directly — verified in tools/relay/main.go). We also
  // set the env var so any child tooling resolves the same dir.
  const relayPid = spawnBg(
    relayBin,
    ["-server", env.PARLAY_SERVER, "-runtime-dir", runtimeDir],
    childEnv,
    join(branchDir, "tools", "relay"),
    relayLog,
  )
  components.push({ kind: "relay", pid: relayPid, port: null, cmd: `${relayBin} -server ${env.PARLAY_SERVER} -runtime-dir ${runtimeDir}`, logFile: relayLog, startedAt: now() })
  log(`relay pid ${relayPid} → runtime ${runtimeDir}`)

  const manifest: SandboxManifest = { version: 1, name: opts.name, createdAt: now(), branchDir, env, components }
  writeManifest(manifest)

  // ── ISOLATION ASSERTIONS — fail loud if any override was not respected ───────
  await assertIsolation(manifest, prodBefore, sandboxDownByManifest)

  log(`sandbox "${opts.name}" UP — manifest at ~/.cache/parlay-split/${opts.name}/manifest.json`)
  return manifest
}

// ── down ─────────────────────────────────────────────────────────────────────

export interface DownResult {
  killed: Array<{ kind: string; pid: number; result: "killed" | "already-dead" | "not-ours" }>
}

/**
 * Verify a live PID's command line still matches the component we recorded.
 * Guards against PID recycling via `ps -o command= -p <pid>` + substring match.
 */
function pidMatchesCmd(pid: number, c: ComponentRecord): boolean {
  const proc = Bun.spawnSync({ cmd: ["ps", "-o", "command=", "-p", String(pid)] })
  if (proc.exitCode !== 0) return false
  const live = proc.stdout.toString().trim()
  // Distinctive token: the server's entry path, or the sandbox-local binary path.
  const token = c.kind === "server" ? "packages/server/src/index.ts" : c.cmd.split(" ")[0]
  return live.includes(token)
}

/**
 * Kill exactly the PIDs recorded for a sandbox. Each PID is verified to still be
 * our process before signaling, so a recycled PID is never killed.
 */
export function sandboxDownByManifest(m: SandboxManifest): DownResult {
  const killed: DownResult["killed"] = []
  // Kill in reverse start order: relay/engine first, server last.
  for (const c of [...m.components].reverse()) {
    if (!pidAlive(c.pid)) {
      killed.push({ kind: c.kind, pid: c.pid, result: "already-dead" })
      continue
    }
    if (!pidMatchesCmd(c.pid, c)) {
      killed.push({ kind: c.kind, pid: c.pid, result: "not-ours" }) // recycled — don't kill
      continue
    }
    try {
      process.kill(c.pid, "SIGTERM")
    } catch {
      /* raced to death */
    }
    killed.push({ kind: c.kind, pid: c.pid, result: "killed" })
  }
  // Give SIGTERM a moment, then SIGKILL any stragglers that are still ours.
  const deadline = Date.now() + 3000
  for (;;) {
    const stragglers = m.components.filter((c) => pidAlive(c.pid) && pidMatchesCmd(c.pid, c))
    if (stragglers.length === 0 || Date.now() >= deadline) {
      for (const c of stragglers) {
        try {
          process.kill(c.pid, "SIGKILL")
        } catch {
          /* gone */
        }
      }
      break
    }
    Bun.sleepSync(120)
  }
  return { killed }
}

export function sandboxDown(name: string): DownResult {
  const m = readManifest(name)
  if (!m) {
    log(`no manifest for sandbox "${name}" — nothing to do`)
    return { killed: [] }
  }
  const res = sandboxDownByManifest(m)
  removeSandboxDir(name)
  log(`sandbox "${name}" DOWN — killed [${res.killed.map((k) => `${k.kind}:${k.pid}:${k.result}`).join(", ")}]`)
  return res
}
