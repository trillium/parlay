#!/usr/bin/env bun
// Bridges `lavish poll <file>` to Parlay chat at 31337.
// True concurrent racing: Parlay (chat) vs 4387 (layout_warnings, session-end, dom_snapshot).
// Whichever has data first wins; the other is aborted. Emits lavish-axi-compatible JSON, then exits.

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
  prompts?: Array<{ tag?: string; text?: string; scenePath?: string; previewPath?: string }>
}
interface ParlayMsg { timeout?: boolean; id?: string; role?: string; text?: string }

// If 4387 is unreachable, resolving to a never-settling promise drops it from the race
// without aborting Parlay or creating a tight restart loop.
function drop<T>(): Promise<T> { return new Promise<T>(() => {}) }

type Prompt = NonNullable<NativeResult["prompts"]>[number]

function nextStep(ended: boolean, prompts: Prompt[] = []): string {
  if (ended) {
    return `The session has ended. Stop polling ${file} — deliver remaining updates in this conversation. Run \`lavish ${file} --reopen\` only if the user explicitly asks for further visual review.`
  }
  const hasWhiteboard = prompts.some(p => p.tag === "whiteboard")
  const whiteboardNote = hasWhiteboard
    ? `This feedback includes whiteboard edits (tag "whiteboard"): read the edit summary in the prompt text first; only open scenePath (.excalidraw JSON) or previewPath (PNG) if the summary isn't enough. Apply edits by updating the Mermaid source in ${file} — Lavish live-reloads it. Never write back to the .excalidraw scene file. `
    : ""
  return `${whiteboardNote}Apply the requested changes to ${file}. Now run \`lavish poll ${file} --agent-reply "<your reply>"\` to send your reply and wait for the next message. Re-running is always safe — queued feedback is never lost.`
}

function emit(result: object): never {
  process.stdout.write(JSON.stringify(result) + "\n")
  process.exit(0)
}

if (agentReply) {
  try {
    await fetch(`${parlay}/api/chat/reply`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: agentReply, agent: agentId, name: agentId, color: "#f4c95d" }),
    })
  } catch (e) {
    process.stderr.write(`lavish-poll: reply post failed: ${e}\n`)
  }
}

const deadline = timeoutMs ? Date.now() + timeoutMs : Infinity
let lastParlayId = ""

while (Date.now() < deadline) {
  const ac = new AbortController()

  // Parlay: long-polls ~30s, returns {timeout:true} on expiry
  const parlayP = fetch(
    `${parlay}/api/chat/poll?after=${encodeURIComponent(lastParlayId)}&channel=${encodeURIComponent(agentId)}`,
    { signal: ac.signal }
  ).then(r => r.json() as Promise<ParlayMsg>).catch(() => ({ timeout: true } as ParlayMsg))

  // 4387: streaming heartbeat mode (no timeoutMs) — holds connection open until data arrives
  const nativeP = fetch(
    `${LAVISH_NATIVE}/api/poll?file=${encodeURIComponent(file)}`,
    { signal: ac.signal }
  ).then(r => r.json() as Promise<NativeResult>).catch(() => null)

  const winner = await Promise.race([
    parlayP.then(v => ({ src: "parlay" as const, v })),
    // Drop from race if 4387 is unreachable (null) — Parlay continues uninterrupted
    nativeP.then(v => v !== null ? { src: "native" as const, v } : drop<{ src: "native"; v: NativeResult }>()),
  ])

  ac.abort()

  if (winner.src === "native") {
    const n = winner.v

    if (n.status === "ended" || n.session_ended) {
      emit({
        session: { file, status: "ended", ...(n.ended_by ? { ended_by: n.ended_by } : {}) },
        dom_snapshot: n.dom_snapshot || "",
        prompts: [],
        next_step: nextStep(true),
      })
    }

    const warnings = n.layout_warnings ?? []
    const prompts = n.prompts ?? []
    if (warnings.length > 0 || prompts.length > 0) {
      emit({
        session: { file, status: "feedback" },
        dom_snapshot: n.dom_snapshot || "",
        prompts,
        ...(warnings.length > 0 ? { layout_warnings: warnings } : {}),
        next_step: nextStep(false, prompts),
      })
    }
    continue // native returned "waiting" (shouldn't happen in streaming mode)
  }

  // Parlay won
  const msg = winner.v
  if (msg.timeout) continue // 30s expired with no chat — restart both

  if (msg.id && msg.role === "user" && msg.text != null) {
    lastParlayId = msg.id
    // 200ms grace window: give aborted nativeP time to deliver dom_snapshot
    const n = await Promise.race([nativeP, Bun.sleep(200).then(() => null)])
    const warnings = n?.layout_warnings ?? []
    const chatPrompt = [{ tag: "chat", text: msg.text }]
    emit({
      session: { file, status: "feedback" },
      dom_snapshot: n?.dom_snapshot || "",
      prompts: chatPrompt,
      ...(warnings.length > 0 ? { layout_warnings: warnings } : {}),
      next_step: nextStep(false, chatPrompt),
    })
  }
}

process.stdout.write(JSON.stringify({
  session: { file, status: "waiting" },
  next_step: `No user feedback arrived before the optional timeout. Run \`lavish poll ${file}\` without --timeout-ms to wait indefinitely — queued feedback is never lost.`,
}) + "\n")
