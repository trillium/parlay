// Shared store helpers for the identity/scratchpad verbs: per-agent directory
// resolution, context.json + frontmatter I/O, the context-reset command probe,
// and the flag tables the dispatcher parses with.

import { mkdirSync, existsSync, readFileSync, writeFileSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import { spawnSync as _spawnSync } from "child_process"
import { EXIT_USAGE } from "../config"
import { die } from "../http"

export type MemKind = "scratchpad" | "identity"

// Boolean + value flag tables for the mem dispatcher (parseArgs consumes both).
export const MEM_BOOL_FLAGS = ["--clear", "--path", "--dry", "--register", "--handoff", "--dismiss-handoff", "--submit", "--park", "--ephemeral", "--preserve", "--reap-ephemeral", "--mint-ephemeral"]
export const MEM_VALUE_FLAGS = ["--agent", "--complete", "--launch", "--name", "--color", "--model", "--cwd", "--rename", "--to", "--older-than", "--mode", "--effort", "--kind", "--yolo", "--worktree", "--project"]

// Root of the per-agent store: ${PARLAY_AGENT_HOME:-~/.parlay/agents}.
// Every id (named, ephemeral, renamed) lives in a <root>/<id>/ directory.
export function agentsRoot(): string {
  return process.env.PARLAY_AGENT_HOME || join(homedir(), ".parlay", "agents")
}

// context.json — {id, name, color} — is the reply-attribution record the panel
// reads. It MUST exist for every id or attribution breaks, so we (re)write it
// whenever an identity's launch spec is seeded or an id is renamed.
export function writeContextJson(dir: string, ctx: { id: string; name?: string; color?: string }): void {
  mkdirSync(dir, { recursive: true })
  const out: Record<string, string> = { id: ctx.id }
  if (ctx.name) out.name = ctx.name
  if (ctx.color) out.color = ctx.color
  writeFileSync(join(dir, "context.json"), JSON.stringify(out, null, 2) + "\n")
}

// The self-restart script was renamed reincarnate → context-reset. The new name is
// not yet on PATH everywhere (only ~/.local/bin/reincarnate is, and that now forwards
// to bin/context-reset via a back-compat wrapper). To keep behavior IDENTICAL today
// while moving call sites to the new name, resolve it lazily: prefer `context-reset`
// if it is resolvable on PATH, otherwise fall back to the legacy `reincarnate` name
// (which is on PATH and execs context-reset). Once a `~/.local/bin/context-reset`
// symlink is deployed, the new name wins with no code change.
export function contextResetCmd(): string {
  // Pass env: process.env explicitly — bun does not propagate process.env mutations
  // to child processes when `env` is omitted (verified bun 1.3.13). Return the
  // ABSOLUTE PATH from `command -v` so callers don't need PATH lookup either.
  const probe = _spawnSync("command", ["-v", "context-reset"], { shell: true, stdio: "pipe", env: process.env })
  if (probe.status === 0) {
    const abs = (probe.stdout as Buffer | null)?.toString().trim()
    if (abs) return abs
  }
  return "reincarnate"
}

// Per-agent memory file under <root>/<id>/<kind>.md, keyed by PARLAY_AGENT_ID
// (or an explicit override). Creates the dir. dies if no identity is resolvable.
export function memFile(kind: MemKind, agentOverride?: string): { agent: string; file: string } {
  const agent = ((agentOverride ?? process.env.PARLAY_AGENT_ID) ?? "").trim()
  if (!agent) die(`parlay ${kind}: no agent identity — run inside a parlay-spawn'd agent (sets PARLAY_AGENT_ID) or pass --agent <id>`, EXIT_USAGE)
  const dir = join(agentsRoot(), agent)
  mkdirSync(dir, { recursive: true })
  return { agent, file: join(dir, `${kind}.md`) }
}

// Identity frontmatter IS the agent's launch spec (id, name, color, model, cwd).
// parlay-spawn seeds it via `identity --register`; `identity --launch <id>` reads it
// and reconstitutes the agent from ONE template. Relaunch = "run the identity".
export function readFrontmatter(file: string): Record<string, string> {
  const txt = existsSync(file) ? readFileSync(file, "utf8") : ""
  const m = txt.match(/^---\n([\s\S]*?)\n---\n/)
  const fm: Record<string, string> = {}
  if (m) for (const line of m[1].split("\n")) {
    const i = line.indexOf(":")
    if (i > 0) fm[line.slice(0, i).trim()] = line.slice(i + 1).trim().replace(/^["']|["']$/g, "")
  }
  return fm
}

export function writeFrontmatter(file: string, fm: Record<string, string>): void {
  const txt = existsSync(file) ? readFileSync(file, "utf8") : ""
  const rest = txt.replace(/^---\n[\s\S]*?\n---\n/, "")
  const lines = Object.entries(fm).filter(([, v]) => v).map(([k, v]) => `${k}: ${/[:#'"\s]/.test(v) ? JSON.stringify(v) : v}`)
  writeFileSync(file, `---\n${lines.join("\n")}\n---\n${rest || `# Identity — ${fm.id ?? ""}\n\n`}`)
}
