// Lightweight developer debug panel — toggle with Ctrl+Shift+D.
// Shows bundle load timing, chat memory estimates, and agent status.
// Self-contained: no external CSS file; uses PA CSS vars with fallbacks.

import { msgs, agentInfo, es, channelStatus } from "./state"

let panelEl: HTMLElement | null = null
let refreshIv: ReturnType<typeof setInterval> | null = null

const B  = (n: number) => n < 1024 ? `${n}B` : n < 1048576 ? `${(n/1024).toFixed(1)}KB` : `${(n/1048576).toFixed(2)}MB`
const MS = (n: number) => n < 1000 ? `${Math.round(n)}ms` : `${(n/1000).toFixed(2)}s`
const ago = (ts: string) => { const d = Date.now() - new Date(ts).getTime(); return d < 60000 ? `${Math.round(d/1000)}s ago` : d < 3600000 ? `${Math.round(d/60000)}m ago` : `${Math.round(d/3600000)}h ago` }

function buildContent(): string {
  const nav = performance.getEntriesByType("navigation")[0] as PerformanceNavigationTiming | undefined

  // Bundles
  const scripts = (performance.getEntriesByType("resource") as PerformanceResourceTiming[]).filter(r => r.name.endsWith(".js"))
  const bundleSection = scripts.length
    ? scripts.map(s => {
        const name = s.name.split("/").pop()!.padEnd(28)
        const size = s.transferSize ? B(s.transferSize) : s.encodedBodySize ? B(s.encodedBodySize) + " (cached)" : "—"
        return `  ${name} ${size.padEnd(16)} ${MS(s.duration)}`
      }).join("\n")
    : "  (no JS resources captured yet)"

  // Chat
  const total   = msgs.length
  const rawSize = total ? JSON.stringify(msgs).length : 0
  const sizes   = msgs.map((m: any) => JSON.stringify(m).length)
  const maxSize = sizes.length ? Math.max(...sizes) : 0
  const avgSize = sizes.length ? Math.round(rawSize / sizes.length) : 0
  const withImg  = msgs.filter((m: any) => m.images?.length).length
  const cards    = msgs.filter((m: any) => m.type === "action_request").length
  const userN    = msgs.filter((m: any) => m.role === "user").length
  const agentN   = msgs.filter((m: any) => m.role === "agent" && m.channel !== "system").length
  const oldestTs = msgs[0]?.ts ? ago(msgs[0].ts) : "—"
  const newestTs = msgs[msgs.length - 1]?.ts ? ago(msgs[msgs.length - 1].ts) : "—"

  // Agents
  const agentLines = agentInfo.size === 0 ? "  (none)"
    : [...agentInfo.values()].map(a => `  ${a.id.padEnd(20)} ${(channelStatus[a.id] ?? "offline").padEnd(10)} ${a.color}`).join("\n")

  // SSE
  const sseState = !es ? "not connected" : ["CONNECTING","OPEN","CLOSED"][es.readyState] ?? "?"

  const lines = [
    "━━ Parlay Debug ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━",
    `  Page: domReady ${nav ? MS(nav.domContentLoadedEventEnd) : "—"}  |  load ${nav ? MS(nav.loadEventEnd) : "—"}  |  uptime ${MS(performance.now())}`,
    "─ Bundles ──────────────────────────────────────────",
    bundleSection,
    "─ Chat ─────────────────────────────────────────────",
    `  ${total} msg(s)  |  est. ${B(rawSize)}  |  avg ${B(avgSize)}  |  largest ${B(maxSize)}`,
    `  user: ${userN}  agent: ${agentN}  |  images: ${withImg}  action_cards: ${cards}`,
    `  oldest: ${oldestTs}  newest: ${newestTs}`,
    `─ Agents (${agentInfo.size}) ──────────────────────────────────────`,
    agentLines,
    "─ SSE ──────────────────────────────────────────────",
    `  ${sseState}`,
    "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━",
    "  Ctrl+Shift+D to close  |  refreshes every 2s",
  ]
  return lines.join("\n")
}

export function toggleDebugPanel(): void {
  if (panelEl) {
    if (refreshIv !== null) { clearInterval(refreshIv); refreshIv = null }
    panelEl.remove(); panelEl = null
    return
  }
  panelEl = document.createElement("pre")
  Object.assign(panelEl.style, {
    position: "fixed", bottom: "12px", right: "12px", zIndex: "9999",
    background: "var(--pa-surf, #0f172a)", color: "var(--pa-body, #e2e8f0)",
    border: "1px solid var(--pa-border, #334155)", borderRadius: "8px",
    padding: "12px 16px", fontFamily: "var(--pa-mono, monospace)", fontSize: "11px",
    lineHeight: "1.6", maxWidth: "min(90vw, 600px)", overflowX: "auto",
    boxShadow: "0 8px 32px rgba(0,0,0,0.5)", whiteSpace: "pre", pointerEvents: "none",
  })
  panelEl.textContent = buildContent()
  document.body.appendChild(panelEl)
  refreshIv = setInterval(() => { if (!panelEl) { clearInterval(refreshIv!); refreshIv = null; return }; panelEl.textContent = buildContent() }, 2000)
}
