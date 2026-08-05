import { readFileSync, writeFileSync, mkdirSync, existsSync, statSync, openSync, readSync, closeSync } from "fs"
import { join } from "path"

// ── Session → agent channel map ──────────────────────────────────────────────
// Processing signals (hook firings, tool activity) are stamped with the Claude
// Code session_id that produced them, never with a Parlay channel. To route
// that processing into the agent's own tab we need session_id → channel.
//
// TWO-LAYER LOOKUP (JSON-primary, command-parsing fallback):
//
// 1. PRIMARY — ~/exchange/parlay-agent-channels.json
//    Agents explicitly declare their channel by writing or POSTing:
//    { "<session_id>": "<channel>" }
//    This is the preferred path — deterministic, no text-parsing, survives
//    Pulse restarts without needing a `parlay monitor` arm first. Agents can
//    declare via the API endpoint POST /api/chat/declare-channel or by writing
//    the JSON file directly.
//
// 2. FALLBACK — tool-activity.jsonl scanning (original behavior)
//    Every agent that runs `parlay monitor --agent <channel>` is captured in
//    tool-activity.jsonl. The tool-activity tailer feeds those lines to
//    recordSessionChannel(); that path still works unchanged for agents that
//    haven't switched to JSON declaration yet.

// Primary: JSON declaration file agents write to explicitly
// Fallback: internal state learned from tool-activity.jsonl parsing
// Both resolved in paths.ts so PARLAY_DATA_DIR can redirect them (robots-jcjj).
import { AGENT_CHANNELS_FILE as DECLARE_PATH, SESSION_CHANNELS_FILE as STATE_PATH } from "./paths"

// In-memory map built from the fallback path
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

// Read the primary JSON declaration file; returns a map of session_id → channel.
// Never throws — returns empty object on any read/parse failure.
function readDeclarations(): Record<string, string> {
  try {
    const raw = readFileSync(DECLARE_PATH, "utf-8")
    const obj = JSON.parse(raw)
    if (obj && typeof obj === "object" && !Array.isArray(obj)) return obj as Record<string, string>
  } catch { /* file absent or malformed — normal on first run */ }
  return {}
}

// Write a session→channel mapping to the primary JSON declaration file.
export function declareChannel(sessionId: string, channel: string): void {
  if (!sessionId || !channel) return
  try {
    mkdirSync(join(DECLARE_PATH, ".."), { recursive: true })
    const existing = readDeclarations()
    // Sticky: first declaration wins; a re-declare only updates if channel matches
    if (!existing[sessionId]) {
      existing[sessionId] = channel
      writeFileSync(DECLARE_PATH, JSON.stringify(existing, null, 2) + "\n", "utf-8")
    }
  } catch { /* best-effort */ }
}

// Expose the declaration path so the router can serve it
export const AGENT_CHANNELS_DECLARE_PATH = DECLARE_PATH

export function recordSessionChannel(sessionId: string | undefined, channel: string | undefined): void {
  if (!sessionId || !channel) return
  // STICKY identity (first-enrollment-wins). A session's agent identity is set
  // at spawn — its first `parlay monitor --agent X`. Later monitors of OTHER
  // channels are that agent WATCHING others (relay/orchestration), NOT it
  // becoming them; letting those overwrite mis-scoped one agent's turns onto
  // another's tab (e.g. firstmate's turns landing on edgar after it monitored
  // edgar). So never overwrite an existing mapping with a different channel.
  const existing = sessionChannel.get(sessionId)
  if (existing) return
  sessionChannel.set(sessionId, channel)
  persist()
}

export function channelForSession(sessionId?: string): string | undefined {
  if (!sessionId) return undefined
  // Primary: check the explicit JSON declaration file first
  const declarations = readDeclarations()
  if (declarations[sessionId]) return String(declarations[sessionId])
  // Fallback: use the in-memory map learned from tool-activity.jsonl parsing
  return sessionChannel.get(sessionId)
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
