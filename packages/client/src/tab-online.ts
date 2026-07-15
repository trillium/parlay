// ── Per-tab online check ──────────────────────────────────────────────────────
// Probes /subscribers after a tab switch to verify the agent is polling.
// Shows a "not listening" banner when it is not. Separated so tabs.ts stays
// under the 250-line limit (single-concept rule).

import { CHAT_BASE } from './config'
import { lastSeenByChannel } from './state'
import { connBanner } from './dom'

export async function checkAgentOnline(ch: string): Promise<void> {
  try {
    const r = await fetch(`${CHAT_BASE}/subscribers`)
    if (!r.ok) return
    const data = await r.json() as {
      poll?: { channels?: { channel: string | null }[] }
      presence?: { channel: string; lastSeen: string | null }[]
    }
    for (const p of data.presence ?? []) {
      if (p.lastSeen) lastSeenByChannel[p.channel] = p.lastSeen
    }
    const pollers = data.poll?.channels ?? []
    const online = pollers.some(p => p.channel === ch)
    if (!online && connBanner) {
      connBanner.className = 'pa-conn-banner reconnecting show'
      connBanner.textContent = `Agent not listening — run: parlay monitor --agent ${ch}`
    }
  } catch {}
}
