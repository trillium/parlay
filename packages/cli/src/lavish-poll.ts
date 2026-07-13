#!/usr/bin/env bun
// Bridges `lavish poll <file>` to Parlay chat at 31337.
// Called by ~/.local/bin/lavish when the first arg is "poll".
// Args: <agentId> <parlayUrl> [--agent-reply <text>] [--timeout-ms <n>] [<file>]
// Emits lavish-axi-compatible JSON on stdout when a user message arrives, then exits.

const [agentId, parlay, ...pollArgs] = process.argv.slice(2)
if (!agentId || !parlay) {
  process.stderr.write("lavish-poll: usage: <agentId> <parlayUrl> [poll-args...]\n")
  process.exit(1)
}

let file = ""
let agentReply: string | undefined
let timeoutMs: number | undefined

for (let i = 0; i < pollArgs.length; i++) {
  if (pollArgs[i] === "--agent-reply") {
    agentReply = pollArgs[++i]
  } else if (pollArgs[i] === "--timeout-ms") {
    timeoutMs = Number(pollArgs[++i])
  } else if (pollArgs[i] && !pollArgs[i].startsWith("--")) {
    file = pollArgs[i]
  }
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
let lastId = ""

while (Date.now() < deadline) {
  try {
    const url = `${parlay}/api/chat/poll?after=${encodeURIComponent(lastId)}&channel=${encodeURIComponent(agentId)}`
    const res = await fetch(url)
    if (!res.ok) { await Bun.sleep(2000); continue }
    const msg = await res.json() as { timeout?: boolean; id?: string; role?: string; text?: string }
    if (msg.timeout) continue
    if (msg.id && msg.role === "user" && msg.text != null) {
      lastId = msg.id
      process.stdout.write(JSON.stringify({
        session: { file, status: "feedback" },
        dom_snapshot: "",
        prompts: [{ tag: "chat", text: msg.text }],
        next_step: `Apply the requested changes to ${file}. Now run \`lavish poll ${file} --agent-reply "<your reply>"\` to send your reply and wait for the next message. Re-running is always safe — queued feedback is never lost.`,
      }) + "\n")
      process.exit(0)
    }
  } catch {
    await Bun.sleep(3000)
  }
}

// Timeout reached without feedback
process.stdout.write(JSON.stringify({
  session: { file, status: "waiting" },
  next_step: `No user feedback arrived before the optional timeout. Run \`lavish poll ${file}\` without --timeout-ms to wait indefinitely — queued feedback is never lost.`,
}) + "\n")
