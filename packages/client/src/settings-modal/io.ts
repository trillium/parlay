import { CHAT_BASE } from '../config'
import { type ParlaySettings, DEFAULTS } from './types'

let _settings: ParlaySettings = { ...DEFAULTS }

export function getSettings(): ParlaySettings { return _settings }

export async function loadSettings(): Promise<ParlaySettings> {
  try {
    const r = await fetch(`${CHAT_BASE}/parlay/settings`, { signal: AbortSignal.timeout(3_000) })
    if (r.ok) _settings = { ...DEFAULTS, ...(await r.json()) }
  } catch { /* server not ready — use defaults */ }
  return _settings
}

export async function saveSettings(s: ParlaySettings): Promise<void> {
  _settings = s
  try {
    await fetch(`${CHAT_BASE}/parlay/settings`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(s),
    })
  } catch {}
}
