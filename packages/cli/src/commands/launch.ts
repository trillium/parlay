import { EXIT_USAGE } from "../config"
import { die, getJSON } from "../http"
import { parseArgs } from "../args"
import { helpWanted } from "../help"
import type { AgentInfo } from "../types"
import { readdirSync, readFileSync } from "fs"
import { homedir } from "os"
import { join } from "path"

// Parse YAML-style frontmatter (--- … ---) from an identity.md file.
// Handles both quoted (`name: "Foo Bar"`) and unquoted values.
function parseFrontmatter(src: string): Record<string, string> {
  const m = src.match(/^---\n([\s\S]*?)\n---/)
  if (!m) return {}
  const out: Record<string, string> = {}
  for (const line of m[1].split("\n")) {
    const kv = line.match(/^(\w+):\s*"?([^"]*)"?\s*$/)
    if (kv) out[kv[1]] = kv[2]
  }
  return out
}

export async function cmdLaunch(args: string[]) {
  if (helpWanted("launch", args)) return
  const { positionals } = parseArgs("launch", args)
  const agentsDir = join(homedir(), ".parlay", "agents")
  type KnownAgent = { id: string; name: string; color: string; cwd: string; model?: string }
  const known: KnownAgent[] = []
  try {
    for (const d of readdirSync(agentsDir, { withFileTypes: true })) {
      if (!d.isDirectory()) continue
      try {
        const fm = parseFrontmatter(readFileSync(join(agentsDir, d.name, "identity.md"), "utf-8"))
        if (fm.id && fm.name && fm.color) known.push({ id: fm.id, name: fm.name, color: fm.color, cwd: fm.cwd || homedir(), ...(fm.model ? { model: fm.model } : {}) })
      } catch { /* no identity.md */ }
    }
  } catch { /* no agents dir */ }

  const targetId = positionals[0]
  if (targetId) {
    const a = known.find(k => k.id === targetId)
    if (!a) return die(`parlay launch: no known agent '${targetId}' — run 'parlay launch' to list available agents`, EXIT_USAGE)
    const revival = "Your context was reset. Follow the recovery chain above (identity → handoff → scratchpad) to restore your state, then await the captain."
    const spawnArgs = [a.id, a.name, a.color, revival, "--cwd", a.cwd, ...(a.model ? ["--model", a.model] : [])]
    process.stderr.write(`parlay launch: spawning ${a.id} via parlay-bin spawn …\n`)
    // bin/parlay-spawn (bash) was ported to tools/parlay-bin (Go, docs/scope-go-spawn.md,
    // ticket A1) exposing `spawn`/`reset` subcommands under one binary — hence the
    // relocated name and the "spawn" subcommand prefix here.
    Bun.spawnSync(["parlay-bin", "spawn", ...spawnArgs], { stdio: ["inherit", "inherit", "inherit"] })
    return
  }

  let live: AgentInfo[] = []
  try { live = await getJSON<AgentInfo[]>("/api/chat/agents") } catch { /* server down or unreachable */ }
  const liveSet = new Set(live.map(a => a.id))
  if (known.length === 0) {
    console.log(`No agent homes found in ${agentsDir}`)
    console.log("Agents are created with: parlay-bin spawn <id> <name> <color> <prompt> [--cwd PATH]")
    return
  }
  const home = homedir()
  const short = (p: string) => p.startsWith(home) ? `~${p.slice(home.length)}` : p
  console.log(`${known.length} known agent(s):`)
  for (const a of known) {
    const status = liveSet.has(a.id) ? "[live]   " : "[offline]"
    console.log(`  ${a.id.padEnd(16)} ${a.name.padEnd(16)} ${a.color}  ${short(a.cwd).padEnd(32)} ${status}`)
  }
  const offline = known.filter(a => !liveSet.has(a.id))
  if (offline.length > 0) {
    process.stderr.write("\nTo launch an offline agent:\n")
    for (const a of offline) process.stderr.write(`  parlay launch ${a.id}\n`)
  }
}
