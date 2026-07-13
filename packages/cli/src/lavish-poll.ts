#!/usr/bin/env bun
// Bridges `lavish poll <file>` to Parlay chat at 31337.
// Races: Parlay for user chat, native 4387 for layout_warnings/session-end/dom_snapshot.
// Emits lavish-axi-compatible JSON on stdout when feedback arrives, then exits.

const LAVISH_NATIVE = process.env.LAVISH_URL ?? "http://127.0.0.1:4387"
const [agentId, parlay, ...pollArgs] = process.argv.slice(2)

if (!agentId || !parlay) {
  process.stderr.write("lavish-poll: usage: <agentId> <parlayUrl> [poll-args...]\n")
  process.exit(1)
}

let file = ""
let agentReply: string | undefined
let timeoutMs: number | undefined

for (let i = 0; i < pollArgs.length; i++) {
  if (pollArgs[i] === "--agent-reply") { agentReply = pollArgs[++i] }
  else if (pollArgs[i] === "--timeout-ms") { timeoutMs = Number(pollArgs[++i]) }
  else if (pollArgs[i] && !pollArgs[i].startsWith("--")) { file = pollArgs[i] }
}

interface NativeResult {
  status: string
  dom_snapshot?: string
  layout_warnings?: unknown[]
  session_ended?: boolean
  ended_by?: string
  prompts?: Array<{ tag?: string; text?: string }>
}

interface ParlayMsg {
  timeout?: boolean; id?: string; role?: string; text?: string
}

async function checkNative(waitMs = 100): Promise<NativeResult | null> {
  try {
    const res = await fetch(
      `${LAVISH_NATIVE}/api/poll?file=${encodeURIComponent(file)}&timeoutMs=${waitMs}`,
      { signal: AbortSignal.timeout(waitMs + 3000) }
    )
    return res.ok ? (await res.json() as NativeResult) : null
  } catch { return null }
}

async function pollParlay(lastId: string): Promise<ParlayMsg | null> {
  try {
    const res = await fetch(
      `${parlay}/api/chat/poll?after=${encodeURIComponent(lastId)}&channel=${encodeURIComponent(agentId)}`,
      { signal: AbortSignal.timeout(40_000) }
    )
    return res.ok ? (await res.json() as ParlayMsg) : null
  } catch { return null }
}

function nextStep(sessionEnded: boolean): string {
  if (sessionEnded) {
    return `The session has ended. Stop polling ${file} — deliver remaining updates directly in this conversation. Run \`lavish ${file} --reopen\` only if the user explicitly asks for further visual review.`
  }
  return `Apply the requested changes to ${file}. Now run \`lavish poll ${file} --agent-reply "<your reply>"\` to send your reply and wait for the next message. Re-running is always safe — queued feedback is never lost.`
}

function emit(result: object): never {
  process.stdout.write(JSON.stringify(result) + "\n")
  process.exit(0)
}

// Post agent reply to Parlay before polling
if (agentReply) {
  try {
    await fetch(`${parlay}/api/chat/reply`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: agentReply, agent: agentId, name: agentId, color: "#f4c95d" }),
    })
  } catch (e) {
    process.stderr.write(`lavish-poll: failed to post agent reply: ${e}\n`)
  }
}

const deadline = timeoutMs ? Date.now() + timeoutMs : Infinity
let lastParlayId = ""

while (Date.now() < deadline) {
  // Check 4387 first for session-end and layout_warnings before blocking on Parlay
  const pre = await checkNative()

  if (pre?.status === "ended" || pre?.session_ended) {
    emit({
      session: { file, status: "ended", ...(pre.ended_by ? { ended_by: pre.ended_by } : {}) },
      dom_snapshot: pre.dom_snapshot || "",
      prompts: [],
      next_step: nextStep(true),
    })
  }

  const preWarnings = pre?.layout_warnings ?? []
  if (preWarnings.length > 0) {
    emit({
      session: { file, status: "feedback" },
      dom_snapshot: pre?.dom_snapshot || "",
      prompts: pre?.prompts?.filter(p => p.tag !== "chat") ?? [],
      layout_warnings: preWarnings,
      next_step: nextStep(false),
    })
  }

  // Long-poll Parlay for user chat (blocks ~30s)
  const msg = await pollParlay(lastParlayId)
  if (msg && !msg.timeout && msg.id && msg.role === "user" && msg.text != null) {
    lastParlayId = msg.id
    // Grab dom_snapshot and any layout_warnings that arrived alongside
    const post = await checkNative()
    const postWarnings = post?.layout_warnings ?? []
    emit({
      session: { file, status: "feedback" },
      dom_snapshot: post?.dom_snapshot || "",
      prompts: [{ tag: "chat", text: msg.text }],
      ...(postWarnings.length > 0 ? { layout_warnings: postWarnings } : {}),
      next_step: nextStep(false),
    })
  }
}

// Deadline reached without feedback
process.stdout.write(JSON.stringify({
  session: { file, status: "waiting" },
  next_step: `No user feedback arrived before the optional timeout. Run \`lavish poll ${file}\` without --timeout-ms to wait indefinitely — queued feedback is never lost.`,
}) + "\n")
