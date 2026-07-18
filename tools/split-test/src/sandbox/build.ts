// Build sandbox-local relay/eval-engine binaries from a checkout's own source,
// and seed untracked-but-runtime-required working files into a checkout.

import { existsSync, mkdirSync, copyFileSync } from "fs"
import { join, resolve } from "path"
import { homedir } from "os"
import { log } from "./common"

/**
 * Build the relay binary from the branch-dir's OWN source into a sandbox-local
 * path. Building per-sandbox (rather than reusing the prod binary) is what makes
 * two-stack a genuine code-level split test: each checkout's relay is compiled.
 */
export function buildRelay(branchDir: string, outDir: string): string {
  const src = join(branchDir, "tools", "relay")
  if (!existsSync(join(src, "main.go"))) {
    throw new Error(`relay source not found at ${src} — is ${branchDir} a parlay checkout?`)
  }
  const out = join(outDir, "parlay-relay")
  log(`building relay from ${src} → ${out}`)
  const proc = Bun.spawnSync({
    cmd: ["go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "."],
    cwd: src,
    env: { ...process.env, CGO_ENABLED: "0" },
    stdout: "pipe",
    stderr: "pipe",
  })
  if (proc.exitCode !== 0) {
    throw new Error(`relay build failed (exit ${proc.exitCode}): ${proc.stderr.toString()}`)
  }
  return out
}

/** Build the eval-engine from source, same rationale as the relay. */
export function buildEngine(branchDir: string, outDir: string): string {
  const src = join(branchDir, "packages", "eval-engine")
  if (!existsSync(join(src, "main.go"))) {
    throw new Error(`eval-engine source not found at ${src}`)
  }
  const out = join(outDir, "parlay-eval-engine")
  log(`building eval-engine from ${src} → ${out}`)
  const proc = Bun.spawnSync({
    cmd: ["go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "."],
    cwd: src,
    env: { ...process.env, CGO_ENABLED: "0" },
    stdout: "pipe",
    stderr: "pipe",
  })
  if (proc.exitCode !== 0) {
    throw new Error(`eval-engine build failed (exit ${proc.exitCode}): ${proc.stderr.toString()}`)
  }
  return out
}

// Untracked-but-runtime-required working files, relative to the repo root. These
// are NOT in git (never committed) yet the server imports them; a fresh worktree
// lacks them. Seed source is the canonical checkout (override with
// PARLAY_SPLIT_SEED_FROM). If neither the branchDir nor the seed source has a
// file, we skip it — the server's parlay-ui.ts already degrades gracefully when
// the .js is absent, and a genuinely missing .ts surfaces as a loud boot failure.
const RUNTIME_SEED_FILES = [
  "packages/server/src/parlay-ui.ts",
  "packages/server/src/parlay-ui.js",
] as const

function seedSource(): string {
  return process.env.PARLAY_SPLIT_SEED_FROM || join(homedir(), "code", "parlay")
}

export function seedRuntimeFiles(branchDir: string): void {
  const src = seedSource()
  if (resolve(src) === resolve(branchDir)) return // seeding into itself is a no-op
  for (const rel of RUNTIME_SEED_FILES) {
    const dst = join(branchDir, rel)
    if (existsSync(dst)) continue // branchDir already has its own copy — respect it
    const from = join(src, rel)
    if (!existsSync(from)) continue // nothing to seed from; skip
    try {
      mkdirSync(join(dst, ".."), { recursive: true })
      copyFileSync(from, dst)
      log(`seeded runtime file ${rel} from ${src}`)
    } catch (err) {
      log(`WARN could not seed ${rel}: ${String(err)}`)
    }
  }
}
