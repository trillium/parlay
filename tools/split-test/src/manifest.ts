// Sandbox manifest + PID bookkeeping under ~/.cache/parlay-split/<name>/.
//
// `down` must kill EXACTLY the processes `up` started — never a broad pkill that
// could catch the prod relay/engine. So `up` records each child PID with the
// component it belongs to and the port it owns; `down` reads that manifest and
// signals only those PIDs, verifying each is really ours before killing.

import { existsSync, mkdirSync, readFileSync, writeFileSync, rmSync, readdirSync } from "fs"
import { join } from "path"
import { homedir } from "os"
import type { ParlayEnv } from "./env"

export const CACHE_ROOT = join(process.env.PARLAY_SPLIT_CACHE || join(homedir(), ".cache", "parlay-split"))

export type ComponentKind = "server" | "relay" | "eval-engine"

export interface ComponentRecord {
  kind: ComponentKind
  pid: number
  port: number | null // relay has no TCP port (unix socket), so null
  cmd: string // argv[0..] joined, for verification + human debugging
  logFile: string
  startedAt: string
}

export interface SandboxManifest {
  version: 1
  name: string
  createdAt: string
  branchDir: string // checkout the components were booted from
  env: ParlayEnv
  components: ComponentRecord[]
}

function sandboxDir(name: string): string {
  return join(CACHE_ROOT, name)
}

function manifestPath(name: string): string {
  return join(sandboxDir(name), "manifest.json")
}

export function sandboxExists(name: string): boolean {
  return existsSync(manifestPath(name))
}

export function ensureSandboxDir(name: string): string {
  const dir = sandboxDir(name)
  mkdirSync(dir, { recursive: true })
  return dir
}

export function logPath(name: string, kind: ComponentKind): string {
  return join(sandboxDir(name), `${kind}.log`)
}

export function pidPath(name: string, kind: ComponentKind): string {
  return join(sandboxDir(name), `${kind}.pid`)
}

export function writeManifest(m: SandboxManifest): void {
  ensureSandboxDir(m.name)
  writeFileSync(manifestPath(m.name), JSON.stringify(m, null, 2) + "\n", "utf8")
  // Also drop per-component .pid files — a human (or an emergency `kill $(cat …)`)
  // can find them without parsing JSON.
  for (const c of m.components) {
    writeFileSync(pidPath(m.name, c.kind), String(c.pid) + "\n", "utf8")
  }
}

export function readManifest(name: string): SandboxManifest | null {
  const p = manifestPath(name)
  if (!existsSync(p)) return null
  try {
    const parsed = JSON.parse(readFileSync(p, "utf8")) as SandboxManifest
    if (parsed.version !== 1 || !Array.isArray(parsed.components)) return null
    return parsed
  } catch {
    return null
  }
}

/** Remove the manifest dir entirely (after a successful `down`). */
export function removeSandboxDir(name: string): void {
  const dir = sandboxDir(name)
  if (existsSync(dir)) rmSync(dir, { recursive: true, force: true })
}

/** List all sandbox names that have a manifest on disk. */
export function listSandboxes(): string[] {
  if (!existsSync(CACHE_ROOT)) return []
  return readdirSync(CACHE_ROOT, { withFileTypes: true })
    .filter((e) => e.isDirectory() && existsSync(manifestPath(e.name)))
    .map((e) => e.name)
    .sort()
}

/**
 * True if `pid` is alive. `process.kill(pid, 0)` throws ESRCH if the process is
 * gone, EPERM if it exists but we can't signal it (still "alive" for our purpose).
 */
export function pidAlive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch (err) {
    return (err as NodeJS.ErrnoException).code === "EPERM"
  }
}
