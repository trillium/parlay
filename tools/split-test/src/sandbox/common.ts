// Shared sandbox helpers: logging, process spawn, prod-PID snapshot, set compare.

import { spawn } from "child_process"
import { openSync } from "fs"

export function log(msg: string): void {
  console.log(`[sandbox] ${msg}`)
}

/** Spawn a detached background process, redirecting stdout+stderr to `logFile`. */
export function spawnBg(cmd: string, args: string[], env: NodeJS.ProcessEnv, cwd: string, logFile: string): number {
  const fd = openSync(logFile, "a")
  const child = spawn(cmd, args, {
    cwd,
    env,
    detached: true,
    stdio: ["ignore", fd, fd],
  })
  if (child.pid === undefined) throw new Error(`spawn failed: ${cmd} ${args.join(" ")}`)
  // Unref so this launcher process can exit while the child keeps running.
  child.unref()
  return child.pid
}

/**
 * Snapshot prod component PIDs BEFORE boot so `up` can prove afterward that it
 * did not disturb them. Returns the pids found for the launchd relay + any
 * running eval-engine.
 */
export function snapshotProdPids(): { relay: number[]; engine: number[] } {
  const grab = (pattern: string): number[] => {
    const proc = Bun.spawnSync({ cmd: ["pgrep", "-f", pattern] })
    if (proc.exitCode !== 0) return []
    return proc.stdout
      .toString()
      .split("\n")
      .map((s) => Number(s.trim()))
      .filter((n) => Number.isInteger(n) && n > 0)
  }
  return {
    relay: grab("Application Support/parlay/bin/parlay-relay"),
    engine: grab("parlay-eval-engine"),
  }
}

export function sameSet(a: number[], b: number[]): boolean {
  if (a.length !== b.length) return false
  const sb = new Set(b)
  return a.every((x) => sb.has(x))
}
