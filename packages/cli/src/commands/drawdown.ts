import { EXIT_USAGE } from "../config"
import { die, getJSON } from "../http"
import { parseArgs } from "../args"
import { fmtMsg, nextStep } from "../format"
import { helpWanted } from "../help"
import type { ChatMessage } from "../types"

// ── parlay drawdown [N] ───────────────────────────────────────────────────────
// Generates a boilerplate handoff prompt from the last N chat messages, ready
// to paste into `handoff create`. Useful when context is running low and the
// agent needs to hand off state to a fresh session.
export async function cmdDrawdown(args: string[]) {
  if (helpWanted("drawdown", args)) return
  const { positionals } = parseArgs("drawdown", args, [])
  const n = positionals[0] ? Number(positionals[0]) : 20
  if (!Number.isFinite(n) || n <= 0) return die("parlay drawdown: N must be a positive number", EXIT_USAGE)

  const msgs = await getJSON<ChatMessage[]>(`/api/chat/history?limit=${n}`)
  const agentId = (process.env.PARLAY_AGENT_ID ?? "").trim() || "<agent-id>"
  const now = new Date().toISOString().slice(0, 19) + "Z"

  // Find last agent message as the "what I was doing" summary.
  const lastAgent = [...msgs].reverse().find(m => m.role === "agent")
  const summary = lastAgent
    ? lastAgent.text?.slice(0, 300).replace(/\n+/g, " ") ?? "(no text)"
    : "(no agent messages in last " + n + ")"

  const body = msgs.length === 0
    ? "(no messages)"
    : msgs.map(m => fmtMsg(m, false)).join("\n")

  console.log(`## Handoff — ${now}

### What I was doing
${summary}

### Recent context (last ${msgs.length} message(s))
\`\`\`
${body}
\`\`\`

### Next steps
[fill in before submitting — what should the next session pick up?]

---
To submit this handoff:
  handoff create "${agentId} context handoff ${now}" --description "<paste body above>"
  identity --submit`)
  nextStep(`identity --submit`)
}
