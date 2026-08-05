import { SERVER, EXIT_USAGE, TRUNCATE_AT } from "../config"
import { die, getJSON, postJSON } from "../http"
import { parseArgs } from "../args"
import { fmtMsg, nextStep } from "../format"
import { helpWanted } from "../help"
import type { AgentInfo, ChatMessage } from "../types"

/** Bounds the pre-flight registry lookup so a wedged server skips the check
 *  rather than hanging the send. */
const REGISTRY_LOOKUP_TIMEOUT_MS = 5_000

/**
 * Aborts the send when `target` is not a registered agent channel.
 *
 * `POST /api/chat/send` accepts any string as `toAgent` and mints a message id
 * for it, so a send to an unregistered channel is a message written where
 * nothing polls — indistinguishable, from the caller's side, from a delivered
 * one (robots-ngg5).
 *
 * Deliberately fails OPEN: an unreadable registry warns and proceeds. This
 * check catches typos and misparsed flags; making every registry hiccup a
 * refused send would trade one lost-message mode for another.
 */
async function requireRegisteredTarget(target: string) {
  let agents: AgentInfo[] | undefined
  try {
    const res = await fetch(`${SERVER}/api/chat/agents`, {
      signal: AbortSignal.timeout(REGISTRY_LOOKUP_TIMEOUT_MS),
    })
    if (res.ok) agents = (await res.json()) as AgentInfo[]
  } catch {
    // fall through to the fail-open branch below
  }

  if (!agents?.length) {
    console.error(`parlay send: could not read the agent registry — sending to "${target}" unverified.`)
    return
  }
  if (agents.some((a) => a.id === target)) return

  const t = target.toLowerCase()
  const near = agents
    .map((a) => a.id)
    .filter((id) => {
      const l = id.toLowerCase()
      return l.includes(t) || t.includes(l)
    })
    .sort()
    .slice(0, 5)

  return die(
    [
      `parlay send: "${target}" is not a registered agent — refusing to send.`,
      "  Nothing polls an unregistered channel, so this message would be accepted and never delivered.",
      ...(near.length ? [`  Did you mean: ${near.join(", ")}`] : []),
      "  Run 'parlay send' with no arguments to list every targetable agent.",
      "  Use --force to send anyway (e.g. seeding a channel before its agent registers).",
    ].join("\n"),
    EXIT_USAGE,
  )
}

export async function cmdSend(args: string[]) {
  if (helpWanted("send", args)) return

  // Separate known flags (--from, --agent/--to, --force) from the dynamic
  // --<agent-id> target flag.
  // `send --mayor "msg"`        → target=mayor, text="msg"
  // `send --agent mayor "msg"`  → same, house-standard spelling
  // `send` (no args)            → list targetable agents
  //
  // robots-ngg5: `--agent` used to fall through to the catch-all below and
  // parse as target "agent", folding the intended recipient into the message
  // BODY — so a steer landed on a phantom channel no relay polls while the
  // caller still got `{ok:true, id}` back. Every other parlay verb spells this
  // `--agent <id>` (`parlay listen --agent <id>`), so it must consume the NEXT
  // token here too.
  let target: string | undefined
  let fromOverride: string | undefined
  let force = false
  const positionals: string[] = []
  for (let i = 0; i < args.length; i++) {
    const a = args[i]
    if (a === "--from") {
      fromOverride = args[++i]
    } else if (a === "--agent" || a === "--to") {
      target = args[++i]
    } else if (a === "--force") {
      force = true
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

  if (!force) await requireRegisteredTarget(target)

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
