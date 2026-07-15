import { readFileSync, writeFileSync, mkdirSync, existsSync, statSync, openSync, readSync, closeSync } from "fs"
import { join } from "path"

// ── Session → agent channel map ──────────────────────────────────────────────
// Processing signals (hook firings, tool activity) are stamped with the Claude
// Code session_id that produced them, never with a Parlay channel. To route
// that processing into the agent's own tab we need session_id → channel.
//
// The map is learned with ZERO new plumbing: every agent enrolls by running
// `parlay monitor --agent <channel>`, and that command is itself captured in
// tool-activity.jsonl as a Monitor/Bash tool_use carrying BOTH the session_id
// and the `--agent <channel>`. The tool-activity tailer feeds those lines to
// recordSessionChannel(); every other firing from that session then resolves.

const STATE_PATH = join(
  process.env.PAI_DIR ?? join(process.env.HOME ?? "", ".claude", "PAI"),
  "MEMORY", "STATE", "parlay-session-channels.json",
)

const sessionChannel = new Map<string, string>()

try {
  const obj = JSON.parse(readFileSync(STATE_PATH, "utf-8")) as Record<string, string>
  for (const [sid, ch] of Object.entries(obj)) {
    if (sid && ch) sessionChannel.set(String(sid), String(ch))
  }
} catch { /* first boot or unreadable — start empty */ }

function persist(): void {
  try {
    mkdirSync(join(STATE_PATH, ".."), { recursive: true })
    writeFileSync(STATE_PATH, JSON.stringify(Object.fromEntries(sessionChannel), null, 2) + "\n", "utf-8")
  } catch { /* best-effort — the map re-learns from enrollment lines anyway */ }
}

export function recordSessionChannel(sessionId: string | undefined, channel: string | undefined): void {
  if (!sessionId || !channel) return
  if (sessionChannel.get(sessionId) === channel) return
  sessionChannel.set(sessionId, channel)
  persist()
}

export function channelForSession(sessionId?: string): string | undefined {
  return sessionId ? sessionChannel.get(sessionId) : undefined
}

// Extract the `--agent <channel>` an enrollment command targets. Matches both
// `--agent foo` and `--agent=foo`; the channel is a kebab/underscore slug.
const ENROLL_RE = /parlay\s+monitor\b[^\n]*?--agent[=\s]+["']?([a-z0-9][a-z0-9_-]*)/i

export function parseEnrollmentChannel(text: string | undefined): string | undefined {
  if (!text) return undefined
  const m = text.match(ENROLL_RE)
  return m ? m[1] : undefined
}

// Startup backfill: the tool-activity tailer resumes at EOF, so it never re-sees
// enrollments that happened before a Pulse restart. Scan the tail of the log
// once at boot so agents that enrolled earlier map immediately, without waiting
// for them to re-arm their monitor. (Disk persistence covers the same case; this
// is the belt to that suspenders, and repairs a lost/empty state file.)
export function backfillFromToolActivity(): void {
  try {
    const path = join(
      process.env.PAI_DIR ?? join(process.env.HOME ?? "", ".claude", "PAI"),
      "MEMORY", "OBSERVABILITY", "tool-activity.jsonl",
    )
    if (!existsSync(path)) return
    const { size } = statSync(path)
    const TAIL = 512 * 1024
    const start = Math.max(0, size - TAIL)
    const fd = openSync(path, "r")
    const buf = Buffer.alloc(size - start)
    readSync(fd, buf, 0, buf.length, start)
    closeSync(fd)
    for (const line of buf.toString("utf8").split("\n")) {
      if (!line || line.indexOf("parlay monitor") === -1) continue
      try {
        const ev = JSON.parse(line)
        if (ev.tool_name !== "Monitor") continue   // real enrollments only, not ps/grep mentions
        const ch = parseEnrollmentChannel(ev.tool_input_preview)
        if (ch) recordSessionChannel(ev.session_id, ch)   // last enrollment wins for a session
      } catch { /* skip partial first line / malformed */ }
    }
  } catch { /* best-effort — live tailing still learns new enrollments */ }
}
