#!/usr/bin/env bun
// parlay — CLI for talking to a Parlay chat server.
// Server URL from PARLAY_SERVER (default http://localhost:4242).
//
// Exit codes: 0 = ok, 1 = runtime/server error, 2 = usage error (bad flag/command/args).

const SERVER = (process.env.PARLAY_SERVER ?? "http://localhost:4242").replace(/\/+$/, "")

const EXIT_RUNTIME = 1
const EXIT_USAGE = 2

const TRUNCATE_AT = 100

interface ChatMessage {
  id: string
  role: "user" | "agent"
  ts: string
  text: string
  channel?: string
  type?: "alert"
}

interface AgentInfo { id: string; name: string; color: string }

interface SubscribersInfo {
  parlay?: { clients?: number }
  poll?: { count?: number; channels?: Array<{ channel: string | null; id?: string; name?: string }> }
  registered?: { count?: number; agents?: AgentInfo[] }
  presence_broadcasts?: number
}

function die(msg: string, code = EXIT_RUNTIME): never {
  console.error(msg)
  process.exit(code)
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

// Parse subcommand args. Boolean flags in `flags`, value-taking flags in `valueFlags`.
// Unknown -x/--x tokens fail loud with exit 2. `--` ends flag parsing.
function parseArgs(
  cmd: string,
  args: string[],
  flags: string[] = [],
  valueFlags: string[] = [],
): { positionals: string[]; opts: Record<string, string | true> } {
  const positionals: string[] = []
  const opts: Record<string, string | true> = {}
  let noMoreFlags = false
  for (let i = 0; i < args.length; i++) {
    const a = args[i]
    if (noMoreFlags || !a.startsWith("-")) { positionals.push(a); continue }
    if (a === "--") { noMoreFlags = true; continue }
    if (flags.includes(a)) { opts[a] = true; continue }
    if (valueFlags.includes(a)) {
      const v = args[++i]
      if (v === undefined) die(`parlay ${cmd}: flag ${a} requires a value`, EXIT_USAGE)
      opts[a] = v
      continue
    }
    die(`parlay ${cmd}: unknown flag "${a}"`, EXIT_USAGE)
  }
  return { positionals, opts }
}

function truncate(text: string, max = TRUNCATE_AT): string {
  const oneLine = text.replace(/\n/g, " ⏎ ")
  if (oneLine.length <= max) return oneLine
  return `${oneLine.slice(0, max)}… (+${oneLine.length - max} chars)`
}

function who(m: ChatMessage): string {
  return m.role === "agent" ? (m.channel ?? "agent") : (m.type === "alert" ? "alert" : "you")
}

function fmtMsg(m: ChatMessage, full: boolean): string {
  const ts = m.ts?.slice(11, 19) ?? ""
  if (full) return `[${ts}] ${who(m).padEnd(12)} id=${m.id} channel=${m.channel ?? "-"}\n  ${m.text}`
  return `[${ts}] ${who(m).padEnd(12)} ${truncate(m.text)}`
}

function nextStep(template: string) {
  console.log(`\nNext: ${template}`)
}

const USAGE = `parlay — talk to a Parlay chat server (${SERVER})

Usage:
  parlay                          Live status: subscribers, agents, last 3 messages
  parlay status                   Same as bare parlay
  parlay subscribers [--full]     Connection counts (--full: raw JSON)
  parlay agents [--full]          List registered agents (--full: raw JSON)
  parlay send <text...>           Send a message from the human to agents
  parlay alert <text...>          Broadcast an alert to all pollers + agents
  parlay alert --agent <id> <text...>   Alert one agent channel
  parlay history [N] [--full]     Last N messages, truncated (default 20; --full: untruncated)
  parlay monitor [--agent <id>]   Persistent poll loop — emits CHAT_MSG lines (for Monitor{})
  parlay lavish-import            Import lavish sessions
  parlay help                     Show this help

Any subcommand accepts --help. Exit codes: 0 ok, 1 server/runtime error, 2 usage error.

Env:
  PARLAY_SERVER   Server base URL (default http://localhost:4242)
`

const HELP: Record<string, string> = {
  status: `parlay status — live snapshot: subscriber counts, agent list, last 3 messages.\nUsage: parlay [status]`,
  subscribers: `parlay subscribers — connection counts (panel clients, pollers, registered agents).\nUsage: parlay subscribers [--full]\n  --full   Print the raw JSON from /api/chat/subscribers`,
  agents: `parlay agents — registered agents, one per line (id, name, color).\nUsage: parlay agents [--full]\n  --full   Print the raw JSON from /api/chat/agents`,
  send: `parlay send — send a message from the human to agents.\nUsage: parlay send <text...>`,
  alert: `parlay alert — broadcast an alert to pollers + agents.\nUsage: parlay alert [--agent <id>] <text...>\n  --agent <id>   Deliver only to one agent channel`,
  history: `parlay history — print recent messages (server-bounded).\nUsage: parlay history [N] [--full]\n  N        How many messages (default 20)\n  --full   Untruncated text plus id and channel per message`,
  monitor: `parlay monitor — persistent poll loop; emits CHAT_MSG|id|role|text lines on stdout.\nUsage: parlay monitor [--agent <id>]\n  --agent <id>   Poll a single agent channel instead of the global feed`,
  "lavish-import": `parlay lavish-import — import lavish sessions via the bundled importer.\nUsage: parlay lavish-import`,
}

function helpWanted(cmd: string, args: string[]): boolean {
  if (!args.includes("--help") && !args.includes("-h")) return false
  console.log(HELP[cmd] ?? USAGE)
  return true
}

async function cmdStatus() {
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

async function cmdSubscribers(args: string[]) {
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

function subChannels(data: SubscribersInfo): string[] {
  return (data.poll?.channels ?? []).map(c => c.channel ?? "(global)")
}

async function cmdAgents(args: string[]) {
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

async function cmdSend(args: string[]) {
  if (helpWanted("send", args)) return
  const { positionals } = parseArgs("send", args)
  const text = positionals.join(" ").trim()
  if (!text) return die("parlay send: message text required", EXIT_USAGE)
  const r = await postJSON<{ ok?: boolean; id?: string; error?: string }>("/api/chat/send", { text })
  if (r.error) return die(`send failed: ${r.error}`)
  console.log(`sent (id ${r.id})`)
  nextStep("parlay history 5")
}

async function cmdAlert(args: string[]) {
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

async function cmdMonitor(args: string[]) {
  if (helpWanted("monitor", args)) return
  const { opts } = parseArgs("monitor", args, [], ["--agent"])
  const agent = opts["--agent"] as string | undefined
  const channelParam = agent ? `&channel=${encodeURIComponent(agent)}` : ""
  let lastId = ""
  process.stderr.write(`parlay monitor — server ${SERVER}${agent ? ` channel ${agent}` : " (global)"}\n`)
  process.stderr.write(`Next (from another shell): parlay send <text...>\n`)
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

async function cmdLavishImport(args: string[]) {
  if (helpWanted("lavish-import", args)) return
  parseArgs("lavish-import", args)
  const { spawnSync } = await import("bun")
  const script = new URL("./lavish-import.ts", import.meta.url).pathname
  spawnSync(["bun", script], { stdio: ["inherit", "inherit", "inherit"], env: process.env })
  nextStep("parlay history 5")
}

async function main() {
  const [cmd, ...args] = process.argv.slice(2)
  switch (cmd) {
    case undefined:
    case "status":        return cmdStatus()
    case "subscribers":   return cmdSubscribers(args)
    case "agents":        return cmdAgents(args)
    case "send":          return cmdSend(args)
    case "alert":         return cmdAlert(args)
    case "history":       return cmdHistory(args)
    case "monitor":       return cmdMonitor(args)
    case "lavish-import": return cmdLavishImport(args)
    case "help":
    case "--help":
    case "-h":            console.log(USAGE); return
    default:
      die(`parlay: unknown command or flag "${cmd}" — run 'parlay help' for usage`, EXIT_USAGE)
  }
}

main()
