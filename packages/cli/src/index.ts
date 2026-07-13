#!/usr/bin/env bun
// parlay — CLI for talking to a Parlay chat server.
// Server URL from PARLAY_SERVER (default http://localhost:4242).

const SERVER = (process.env.PARLAY_SERVER ?? "http://localhost:4242").replace(/\/+$/, "")

interface ChatMessage {
  id: string
  role: "user" | "agent"
  ts: string
  text: string
  channel?: string
  type?: "alert"
}

interface AgentInfo { id: string; name: string; color: string }

function die(msg: string): never {
  console.error(msg)
  process.exit(1)
}

async function getJSON<T>(path: string): Promise<T> {
  let res: Response
  try {
    res = await fetch(`${SERVER}${path}`)
  } catch (err) {
    return die(`Cannot reach Parlay server at ${SERVER} — ${String(err)}`)
  }
  if (!res.ok) return die(`GET ${path} failed: ${res.status} ${res.statusText}`)
  return res.json() as Promise<T>
}

async function postJSON<T>(path: string, body: unknown): Promise<T> {
  let res: Response
  try {
    res = await fetch(`${SERVER}${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    })
  } catch (err) {
    return die(`Cannot reach Parlay server at ${SERVER} — ${String(err)}`)
  }
  if (!res.ok) return die(`POST ${path} failed: ${res.status} ${res.statusText}`)
  return res.json() as Promise<T>
}

function fmtMsg(m: ChatMessage): string {
  const who = m.role === "agent" ? (m.channel ?? "agent") : (m.type === "alert" ? "alert" : "you")
  const ts = m.ts?.slice(11, 19) ?? ""
  return `[${ts}] ${who.padEnd(12)} ${m.text}`
}

const USAGE = `parlay — talk to a Parlay chat server (${SERVER})

Usage:
  parlay subscribers              List connected browser tabs, pollers, and agents
  parlay agents                   List registered agents
  parlay send <text...>           Send a message from the human to agents
  parlay alert <text...>          Broadcast an alert to all pollers + agents
  parlay alert --agent <id> <text...>   Alert one agent channel
  parlay history [N]              Print the last N messages (default 20)
  parlay monitor [--agent <id>]   Persistent poll loop — emits CHAT_MSG lines (for Monitor{})
  parlay help                     Show this help

Env:
  PARLAY_SERVER   Server base URL (default http://localhost:4242)
`

async function cmdSubscribers() {
  const data = await getJSON<unknown>("/api/chat/subscribers")
  console.log(JSON.stringify(data, null, 2))
}

async function cmdAgents() {
  const agents = await getJSON<AgentInfo[]>("/api/chat/agents")
  if (agents.length === 0) { console.log("No registered agents."); return }
  for (const a of agents) console.log(`${a.id.padEnd(20)} ${a.name.padEnd(20)} ${a.color}`)
}

async function cmdSend(args: string[]) {
  const text = args.join(" ").trim()
  if (!text) return die("send: message text required")
  const r = await postJSON<{ ok?: boolean; id?: string; error?: string }>("/api/chat/send", { text })
  if (r.error) return die(`send failed: ${r.error}`)
  console.log(`sent (id ${r.id})`)
}

async function cmdAlert(args: string[]) {
  let agent: string | undefined
  const rest: string[] = []
  for (let i = 0; i < args.length; i++) {
    if (args[i] === "--agent") {
      agent = args[++i]
      if (!agent) return die("alert: --agent requires an id")
    } else {
      rest.push(args[i])
    }
  }
  const text = rest.join(" ").trim()
  if (!text) return die("alert: message text required")
  const body = agent ? { text, agents: [agent] } : { text }
  const r = await postJSON<{ ok?: boolean; channels?: number; delivered?: number; error?: string }>("/api/chat/alert", body)
  if (r.error) return die(`alert failed: ${r.error}`)
  console.log(`alert sent to ${r.channels} channel(s), delivered to ${r.delivered} live poller(s)`)
}

async function cmdMonitor(args: string[]) {
  let agent: string | undefined
  for (let i = 0; i < args.length; i++) {
    if (args[i] === "--agent") { agent = args[++i]; if (!agent) return die("monitor: --agent requires an id") }
  }
  const channelParam = agent ? `&channel=${encodeURIComponent(agent)}` : ""
  let lastId = ""
  process.stderr.write(`parlay monitor — server ${SERVER}${agent ? ` channel ${agent}` : " (global)"}\n`)
  while (true) {
    try {
      const res = await fetch(`${SERVER}/api/chat/poll?after=${lastId}${channelParam}`)
      if (!res.ok) { await Bun.sleep(2000); continue }
      const msg = await res.json() as { timeout?: boolean; id?: string; role?: string; text?: string }
      if (msg.timeout) continue
      if (msg.id && msg.role && msg.text != null) {
        lastId = msg.id
        process.stdout.write(`CHAT_MSG|${msg.id}|${msg.role}|${msg.text}\n`)
      }
    } catch {
      await Bun.sleep(3000)
    }
  }
}

async function cmdHistory(args: string[]) {
  const n = args[0] ? Number(args[0]) : 20
  if (!Number.isFinite(n) || n <= 0) return die("history: N must be a positive number")
  const all = await getJSON<ChatMessage[]>("/api/chat/history")
  const slice = all.slice(-n)
  if (slice.length === 0) { console.log("No messages yet."); return }
  for (const m of slice) console.log(fmtMsg(m))
}

async function main() {
  const [cmd, ...args] = process.argv.slice(2)
  switch (cmd) {
    case "subscribers": return cmdSubscribers()
    case "agents":      return cmdAgents()
    case "send":        return cmdSend(args)
    case "alert":       return cmdAlert(args)
    case "history":     return cmdHistory(args)
    case "monitor":     return cmdMonitor(args)
    case "help":
    case "--help":
    case "-h":
    case undefined:     console.log(USAGE); return
    default:
      console.error(`Unknown command: ${cmd}\n`)
      console.log(USAGE)
      process.exit(1)
  }
}

main()
