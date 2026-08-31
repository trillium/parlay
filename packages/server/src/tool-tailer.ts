import { existsSync, statSync, openSync, readSync, closeSync } from "fs"
import { join } from "path"
import { pushHubEvent } from "./hub-ingress"
import { recordSessionChannel, channelForSession, parseEnrollmentChannel } from "./session-channel"
import { TOOL_EVENT } from "./tool-event"

// ── Tool event tailer ───────────────────────────────────────────────────────
// Tails tool-activity.jsonl and broadcasts each new entry as a tool_event SSE,
// tagged with the agent channel that produced it so the panel can scope the
// tool log per tab instead of showing one global firehose. Each entry carries
// a session_id; the enrollment command (`parlay monitor --agent <ch>`) that
// every agent runs is itself captured here, which is how session → channel is
// learned (see session-channel.ts).
//
// The broadcast goes out over HTTP (POST /api/chat/events on the Go server)
// rather than this process's own SSE client map: the file being tailed lives in
// the TS/Pulse home so the tailer stays here, but the panel's SSE connection is
// served by the Go hub. See hub-ingress.ts — the push is fire-and-forget and an
// unreachable hub must never stop the tail loop.

export function startToolEventTailer() {
  const HOME           = process.env.HOME ?? ""
  const PAI_DIR        = process.env.PAI_DIR ?? join(HOME, ".claude", "PAI")
  const TOOL_ACTIVITY  = join(PAI_DIR, "MEMORY", "OBSERVABILITY", "tool-activity.jsonl")
  if (!existsSync(TOOL_ACTIVITY)) return

  let byteOffset = 0
  try { byteOffset = statSync(TOOL_ACTIVITY).size } catch { return }  // start from current EOF

  setInterval(() => {
    try {
      const { size } = statSync(TOOL_ACTIVITY)
      if (size <= byteOffset) return
      const fd  = openSync(TOOL_ACTIVITY, "r")
      const buf = Buffer.alloc(size - byteOffset)
      readSync(fd, buf, 0, buf.length, byteOffset)
      closeSync(fd)
      byteOffset = size
      for (const line of buf.toString("utf8").split("\n").filter(Boolean)) {
        try {
          const ev   = JSON.parse(line)
          const gt   = ev.ground_truth ?? {}
          const inp  = ev.tool_input_preview ? (() => { try { return JSON.parse(ev.tool_input_preview) } catch { return {} } })() : {}
          // Learn session → channel from the agent's own enrollment BEFORE
          // attributing this (or any later) event. Enrollment is armed through
          // the Monitor tool (the pulse-agent contract), so only Monitor events
          // count — a Bash line that merely mentions `parlay monitor --agent x`
          // (ps/grep/kill housekeeping) must never remap a session.
          if (ev.tool_name === "Monitor") {
            const enrolled = parseEnrollmentChannel(ev.tool_input_preview)
            if (enrolled) recordSessionChannel(ev.session_id, enrolled)
          }
          const desc = gt.description ?? inp.description ?? inp.file_path ?? inp.url ?? ""
          const cmd  = (gt.command ?? "").slice(0, 140)
          const out  = (gt.stdout_preview ?? "").slice(0, 280)
          // Owning tab: the enrolling agent's channel, else the shared System
          // pseudo-tab for sessions that never enrolled (non-agent Claude runs).
          const channel = channelForSession(ev.session_id) ?? "system"
          pushHubEvent(TOOL_EVENT, {
            ts:   ev.timestamp,
            tool: ev.tool_name ?? "?",
            desc: desc.slice(0, 100),
            cmd,
            out,
            err:  (gt.stderr_preview ?? "").slice(0, 120),
            channel,
          })
        } catch { /* skip malformed lines */ }
      }
    } catch { /* file may be rotating */ }
  }, 500)
}
