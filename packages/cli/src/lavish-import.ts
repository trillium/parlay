#!/usr/bin/env bun
// One-shot: import existing Lavish session chat history into Parlay.
// Reads /events/<key> SSE from Lavish at 4387, replays into Parlay at 31337.

const LAVISH  = process.env.LAVISH_URL  ?? "http://127.0.0.1:4387"
const PARLAY  = process.env.PARLAY_SERVER ?? "http://localhost:31337"
const STATE   = `${process.env.HOME}/.lavish-axi/state.json`

interface LavishMsg { role: "agent" | "user"; text: string; at: string }

async function fetchChatHistory(key: string): Promise<LavishMsg[]> {
  const res = await fetch(`${LAVISH}/events/${key}`)
  const reader = res.body!.getReader()
  const dec = new TextDecoder()
  let buf = ""
  while (true) {
    const { done, value } = await Promise.race([
      reader.read(),
      new Promise<{ done: true; value: undefined }>(r => setTimeout(() => r({ done: true, value: undefined }), 4000)),
    ])
    if (done) break
    buf += dec.decode(value, { stream: true })
    const match = buf.match(/^data: (.+)$/m)
    if (match) {
      reader.cancel()
      const parsed = JSON.parse(match[1]) as { chat?: LavishMsg[] }
      return parsed.chat ?? []
    }
  }
  return []
}

async function replayToParlay(key: string, msgs: LavishMsg[], file: string) {
  console.log(`\n[${key.slice(0,8)}] ${file} — ${msgs.length} message(s)`)
  for (const m of msgs) {
    const ts = m.at?.slice(11, 19) ?? "?"
    if (m.role === "agent") {
      const r = await fetch(`${PARLAY}/api/chat/reply`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text: m.text, agent: "lavish", name: "Lavish", color: "#f4c95d" }),
      })
      console.log(`  [${ts}] agent → ${r.ok ? "ok" : `FAIL ${r.status}`} — ${m.text.slice(0, 60)}…`)
    } else {
      const r = await fetch(`${PARLAY}/api/chat/send`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text: m.text }),
      })
      console.log(`  [${ts}] user  → ${r.ok ? "ok" : `FAIL ${r.status}`} — ${m.text.slice(0, 60)}…`)
    }
  }
}

const stateRaw = JSON.parse(await Bun.file(STATE).text())
const sessions = Object.entries(stateRaw.sessions as Record<string, { status: string; file: string }>)
  .filter(([, s]) => s.status === "open")

if (sessions.length === 0) { console.log("No open Lavish sessions."); process.exit(0) }
console.log(`Found ${sessions.length} open session(s). Importing into Parlay at ${PARLAY}…`)

for (const [key, s] of sessions) {
  const file = s.file.split("/").pop() ?? key
  try {
    const msgs = await fetchChatHistory(key)
    if (msgs.length === 0) { console.log(`[${key.slice(0,8)}] no messages`); continue }
    await replayToParlay(key, msgs, file)
  } catch (e) {
    console.error(`[${key.slice(0,8)}] error: ${e}`)
  }
}
console.log("\nDone.")
