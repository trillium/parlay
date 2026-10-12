// Connected-devices view (Ctrl+Shift+V): every page that announced itself via
// the client roll call (roll-call.ts), so a phone and a laptop on the same
// session are both visible. Read-only; refreshes every 3s while open.
import { CHAT_BASE } from './config'
import { getDeviceId } from './sse'

export interface DeviceRow {
  clientId: string
  stale: boolean
  lastSeen: string
  state: { device?: string; deviceLabel?: string; version?: string; loadAddress?: string; voiceArmed?: boolean; target?: string }
}

const ago = (ts: string, now = Date.now()) => {
  const d = Math.max(0, now - new Date(ts).getTime())
  return d < 60_000 ? `${Math.round(d / 1000)}s ago` : d < 3_600_000 ? `${Math.round(d / 60_000)}m ago` : `${Math.round(d / 3_600_000)}h ago`
}

/** Plain-text rows, one device per line; `selfId` marks the viewing browser. */
export function renderDevices(rows: DeviceRow[], selfId: string, now = Date.now()): string[] {
  if (!rows.length) return ['no device has announced itself']
  return rows.map((r) => {
    const s = r.state
    const parts = [
      s.deviceLabel || s.device || r.clientId,
      s.device && s.device === selfId ? '(this device)' : '',
      r.stale ? 'STALE' : 'live',
      `seen ${ago(r.lastSeen, now)}`,
      s.version ? `v${s.version}` : '',
      s.voiceArmed ? 'voice armed' : '',
      s.target ? `→ ${s.target}` : '',
    ]
    return parts.filter(Boolean).join(' · ')
  })
}

let panelEl: HTMLElement | null = null
let iv: ReturnType<typeof setInterval> | null = null

async function refresh() {
  if (!panelEl) return
  let lines: string[]
  try {
    const res = await fetch(`${CHAT_BASE}/clients`, { headers: { accept: 'application/json' } })
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = (await res.json()) as { connected: number; clients: DeviceRow[] }
    lines = [`Connected devices (${data.clients.filter((c) => !c.stale).length} live)`, ...renderDevices(data.clients, getDeviceId())]
  } catch (e) {
    lines = ['Connected devices', `could not load: ${(e as Error).message}`]
  }
  if (panelEl) panelEl.textContent = [...lines, '', 'Ctrl+Shift+V to close'].join('\n')
}

export function toggleDevicesPanel(): void {
  if (panelEl) {
    if (iv !== null) { clearInterval(iv); iv = null }
    panelEl.remove(); panelEl = null
    return
  }
  panelEl = document.createElement('pre')
  Object.assign(panelEl.style, {
    position: 'fixed', bottom: '12px', left: '12px', zIndex: '9999',
    background: 'var(--pa-surf, #0f172a)', color: 'var(--pa-body, #e2e8f0)',
    border: '1px solid var(--pa-border, #334155)', borderRadius: '8px',
    padding: '12px 16px', fontFamily: 'var(--pa-mono, monospace)', fontSize: '11px',
    lineHeight: '1.6', maxWidth: 'min(90vw, 600px)', overflowX: 'auto',
    boxShadow: '0 8px 32px rgba(0,0,0,0.5)', whiteSpace: 'pre-wrap', pointerEvents: 'none',
  })
  panelEl.textContent = 'Connected devices\nloading…'
  document.body.appendChild(panelEl)
  void refresh()
  iv = setInterval(() => void refresh(), 3000)
}
