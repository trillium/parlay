// parlay CLI command handlers: one function per subcommand.

import { SERVER, EXIT_USAGE, TRUNCATE_AT } from "./config"
import { die, getJSON, postJSON } from "./http"
import { parseArgs } from "./args"
import { fmtMsg, nextStep } from "./format"
import { helpWanted } from "./help"
import { runMonitor } from "./monitor"
import type { AgentInfo, ChatMessage, SubscribersInfo } from "./types"

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

export async function cmdLavishImport(args: string[]) {
  if (helpWanted("lavish-import", args)) return
  parseArgs("lavish-import", args)
  const { spawnSync } = await import("bun")
  const script = new URL("./lavish-import.ts", import.meta.url).pathname
  spawnSync(["bun", script], { stdio: ["inherit", "inherit", "inherit"], env: process.env })
  nextStep("parlay history 5")
}
