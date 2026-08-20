import { existsSync, statSync, openSync, readSync, closeSync } from "fs"
import { join } from "path"
import { postHubMessage } from "./hub-ingress"
import { channelForSession } from "./session-channel"

// ── Hook firing tailer ──────────────────────────────────────────────────────
// Tails MEMORY/OBSERVABILITY/hook-firings.jsonl (written synchronously by
// hooks via hooks/lib/parlay-announce.ts logHookFiring) and turns each entry
// into a system_update chat message — persisted to history and broadcast over
// SSE. This is how hook activity becomes visible in the Parlay panel without
// hooks ever paying network latency.
//
// Persist-and-broadcast now happens on the Go server (POST /api/chat/message,
// which does both) instead of this process's in-process addMessage: the file
// being tailed lives in the TS/Pulse home so the tailer stays here, while both
// the history file and the panel's SSE connection move to Go. The message shape
// is unchanged — type/source/meta ride along in the POST body. See
// hub-ingress.ts; the post is fire-and-forget and an unreachable hub must never
// stop the tail loop.

export function startHookFiringTailer() {
  const HOME         = process.env.HOME ?? ""
  const PAI_DIR      = process.env.PAI_DIR ?? join(HOME, ".claude", "PAI")
  const HOOK_FIRINGS = join(PAI_DIR, "MEMORY", "OBSERVABILITY", "hook-firings.jsonl")

  let byteOffset = existsSync(HOOK_FIRINGS) ? (() => {
    try { return statSync(HOOK_FIRINGS).size } catch { return 0 }
  })() : 0

  setInterval(() => {
    try {
      if (!existsSync(HOOK_FIRINGS)) return
      const { size } = statSync(HOOK_FIRINGS)
      if (size < byteOffset) byteOffset = 0        // rotated/truncated — restart
      if (size <= byteOffset) return
      const fd  = openSync(HOOK_FIRINGS, "r")
      const buf = Buffer.alloc(size - byteOffset)
      readSync(fd, buf, 0, buf.length, byteOffset)
      closeSync(fd)
      byteOffset = size
      for (const line of buf.toString("utf8").split("\n").filter(Boolean)) {
        try {
          const ev = JSON.parse(line)
          const source = String(ev.source ?? "hook").slice(0, 60)
          // Ordinary firings are short; agent turn mirrors (source "turn") can
          // run long, so allow up to 1400 chars for readable deliberations.
          const text   = String(ev.text ?? "").slice(0, 1400)
          if (!text) continue
          // Route the firing to the tab of the agent whose session produced it,
          // resolved via the session → channel map. Firings without a known
          // session (most hooks don't stamp session_id yet) fall back to the
          // shared 'system' pseudo-tab. session_id still rides in meta.
          const channel = channelForSession(ev.session_id ? String(ev.session_id) : undefined) ?? "system"
          postHubMessage("agent", text, channel, {
            type: "system_update",
            source,
            ...(ev.session_id ? { meta: { session_id: String(ev.session_id) } } : {}),
          })
        } catch { /* skip malformed lines */ }
      }
    } catch { /* file may be rotating */ }
  }, 1000)
}
