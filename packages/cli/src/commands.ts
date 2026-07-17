// parlay CLI command handlers: one function per subcommand.

import { SERVER, EXIT_USAGE, TRUNCATE_AT } from "./config"
import { die, getJSON, postJSON } from "./http"
import { parseArgs } from "./args"
import { fmtMsg, nextStep } from "./format"
import { helpWanted } from "./help"
import { runMonitor } from "./monitor"
import type { AgentInfo, ChatMessage, SubscribersInfo } from "./types"
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

export async function cmdStatus() {
  const [subs, agents, recent] = await Promise.all([
    getJSON<SubscribersInfo>("/api/chat/subscribers"),
    getJSON<AgentInfo[]>("/api/chat/agents"),
    getJSON<ChatMessage[]>("/api/chat/history?limit=3"),
  ])
  const clients = subs.parlay?.clients ?? 0
  const pollers = subs.poll?.count ?? 0
  console.log(`parlay @ ${SERVER}`)
  console.log(`subscribers: ${clients} panel client(s), ${pollers} poller(s)`)
  if (agents.length === 0) console.log("agents: 0 registered")
  else console.log(`agents (${agents.length}): ${agents.map(a => a.id).join(", ")}`)
  if (recent.length === 0) console.log("messages: 0 messages")
  else {
    console.log(`last ${recent.length} message(s):`)
    for (const m of recent) console.log(`  ${fmtMsg(m, false)}`)
  }
  nextStep("parlay history 20")
}

function subChannels(data: SubscribersInfo): string[] {
  return (data.poll?.channels ?? []).map(c => c.channel ?? "(global)")
}

export async function cmdSubscribers(args: string[]) {
  if (helpWanted("subscribers", args)) return
  const { opts } = parseArgs("subscribers", args, ["--full"])
  const data = await getJSON<SubscribersInfo>("/api/chat/subscribers")
  if (opts["--full"]) {
    console.log(JSON.stringify(data, null, 2))
    process.stderr.write("\nNext: parlay agents\n")
    return
  }
  const channels = (subChannels(data)).join(", ")
  console.log(`panel clients: ${data.parlay?.clients ?? 0}`)
  console.log(`pollers: ${data.poll?.count ?? 0}${channels ? ` (${channels})` : ""}`)
  console.log(`registered agents: ${data.registered?.count ?? 0}`)
  if (data.presence_broadcasts !== undefined) console.log(`presence broadcasts: ${data.presence_broadcasts}`)
  nextStep("parlay agents")
}

export async function cmdAgents(args: string[]) {
  if (helpWanted("agents", args)) return
  const { opts } = parseArgs("agents", args, ["--full"])
  const agents = await getJSON<AgentInfo[]>("/api/chat/agents")
  if (opts["--full"]) {
    console.log(JSON.stringify(agents, null, 2))
    process.stderr.write("\nNext: parlay alert --agent <id> <text...>\n")
    return
  }
  if (agents.length === 0) {
    console.log("0 agents registered.")
  } else {
    console.log(`${agents.length} agent(s):`)
    for (const a of agents) console.log(`${a.id.padEnd(20)} ${a.name.padEnd(20)} ${a.color}`)
  }
  nextStep("parlay alert --agent <id> <text...>")
}

export async function cmdSend(args: string[]) {
  if (helpWanted("send", args)) return

  // Separate known flags (--from) from the dynamic --<agent-id> target flag.
  // `send --mayor "msg"` → target=mayor, text="msg"
  // `send` (no args)    → list targetable agents
  let target: string | undefined
  let fromOverride: string | undefined
  const positionals: string[] = []
  for (let i = 0; i < args.length; i++) {
    const a = args[i]
    if (a === "--from") {
      fromOverride = args[++i]
    } else if (a.startsWith("--")) {
      // Any unrecognized --flag is the target agent id (e.g. --mayor → "mayor")
      target = a.slice(2)
    } else {
      positionals.push(a)
    }
  }

  // No args at all → list targetable agents
  if (!target && positionals.length === 0) {
    const agents = await getJSON<AgentInfo[]>("/api/chat/agents")
    if (agents.length === 0) {
      console.log("0 agents registered — no one to send to.")
    } else {
      console.log(`${agents.length} agent(s) you can message:`)
      for (const a of agents) {
        console.log(`  send --${a.id.padEnd(22)} # → ${a.name}`)
      }
    }
    return
  }

  const text = positionals.join(" ").trim()
  if (!text) return die(`parlay send: message text required (e.g. send --${target ?? "<agent-id>"} "your message")`, EXIT_USAGE)
  if (!target) return die("parlay send: no target agent — use send --<agent-id> \"msg\" or bare send to list agents", EXIT_USAGE)

  // Auto-fill from PARLAY_AGENT_ID so agent→agent attribution works with no boilerplate.
  const from = (fromOverride ?? process.env.PARLAY_AGENT_ID ?? "").trim()
  const body: Record<string, unknown> = { text, toAgent: target, ...(from ? { from } : {}) }
  const r = await postJSON<{ ok?: boolean; id?: string; error?: string }>("/api/chat/send", body)
  if (r.error) return die(`send failed: ${r.error}`)
  console.log(`sent to ${target}${from ? ` (from ${from})` : ""} — id ${r.id}`)
  nextStep(`parlay history 5`)
}

export async function cmdAlert(args: string[]) {
  if (helpWanted("alert", args)) return
  const { positionals, opts } = parseArgs("alert", args, [], ["--agent"])
  const agent = opts["--agent"] as string | undefined
  const text = positionals.join(" ").trim()
  if (!text) return die("parlay alert: message text required", EXIT_USAGE)
  const body = agent ? { text, agents: [agent] } : { text }
  const r = await postJSON<{ ok?: boolean; channels?: number; delivered?: number; error?: string }>("/api/chat/alert", body)
  if (r.error) return die(`alert failed: ${r.error}`)
  console.log(`alert sent to ${r.channels} channel(s), delivered to ${r.delivered} live poller(s)`)
  nextStep("parlay subscribers")
}

export async function cmdMonitor(args: string[]) {
  return runMonitor(args, { server: SERVER, exitUsage: EXIT_USAGE, die, helpWanted, parseArgs })
}

export async function cmdHistory(args: string[]) {
  if (helpWanted("history", args)) return
  const { positionals, opts } = parseArgs("history", args, ["--full"])
  const n = positionals[0] ? Number(positionals[0]) : 20
  if (!Number.isFinite(n) || n <= 0) return die("parlay history: N must be a positive number", EXIT_USAGE)
  const full = opts["--full"] === true
  const msgs = await getJSON<ChatMessage[]>(`/api/chat/history?limit=${n}`)
  if (msgs.length === 0) {
    console.log("0 messages.")
  } else {
    for (const m of msgs) console.log(fmtMsg(m, full))
    if (!full) console.log(`(${msgs.length} message(s), text truncated at ${TRUNCATE_AT} chars — use --full)`)
  }
  nextStep("parlay send <text...>")
}

export async function cmdStats(args: string[]) {
  if (helpWanted("stats", args)) return
  const [msgs, agents] = await Promise.all([
    getJSON<ChatMessage[]>("/api/chat/history?limit=2000"),
    getJSON<AgentInfo[]>("/api/chat/agents"),
  ])
  if (!msgs.length) { console.log("0 messages."); nextStep("parlay history 20"); return }
  const B = (n: number) => n < 1024 ? `${n}B` : `${(n / 1024).toFixed(1)}KB`
  const sizes   = msgs.map(m => JSON.stringify(m).length)
  const total   = sizes.reduce((a, b) => a + b, 0)
  const largest = Math.max(...sizes)
  const avg     = Math.round(total / sizes.length)
  const userN   = msgs.filter(m => m.role === "user").length
  const agentN  = msgs.filter(m => m.role === "agent").length
  const imgN    = (msgs as any[]).filter(m => m.images?.length).length
  const cardN   = (msgs as any[]).filter(m => m.type === "action_request").length
  const oldest  = new Date(msgs[0].ts).toLocaleString()
  const newest  = new Date(msgs[msgs.length - 1].ts).toLocaleString()
  console.log(`messages: ${msgs.length}  |  est. ${B(total)}  |  avg ${B(avg)}  |  largest ${B(largest)}`)
  console.log(`  user: ${userN}  agent: ${agentN}  |  images: ${imgN}  action_cards: ${cardN}`)
  console.log(`  oldest: ${oldest}`)
  console.log(`  newest: ${newest}`)
  console.log(`agents: ${agents.length} registered`)
  nextStep("Ctrl+Shift+D in the panel for client-side bundle/memory breakdown")
}

export async function cmdLavishImport(args: string[]) {
  if (helpWanted("lavish-import", args)) return
  parseArgs("lavish-import", args)
  const { spawnSync } = await import("bun")
  const script = new URL("./lavish-import.ts", import.meta.url).pathname
  spawnSync(["bun", script], { stdio: ["inherit", "inherit", "inherit"], env: process.env })
  nextStep("parlay history 5")
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
    process.stderr.write(`parlay launch: spawning ${a.id} via parlay-spawn …\n`)
    Bun.spawnSync(["parlay-spawn", ...spawnArgs], { stdio: ["inherit", "inherit", "inherit"] })
    return
  }

  let live: AgentInfo[] = []
  try { live = await getJSON<AgentInfo[]>("/api/chat/agents") } catch { /* server down or unreachable */ }
  const liveSet = new Set(live.map(a => a.id))
  if (known.length === 0) {
    console.log(`No agent homes found in ${agentsDir}`)
    console.log("Agents are created with: parlay-spawn <id> <name> <color> <prompt> [--cwd PATH]")
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
