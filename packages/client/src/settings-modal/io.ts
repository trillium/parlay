import { CHAT_BASE } from '../config'
import { type ParlaySettings, DEFAULTS } from './types'

let _settings: ParlaySettings = { ...DEFAULTS }

// Bumped on every load/save — lets caches keyed on settings (e.g. compiled
// command matchers, #20) invalidate without deep comparison
let _settingsVersion = 0
export function getSettingsVersion(): number { return _settingsVersion }

export function getSettings(): ParlaySettings { return _settings }

export async function loadSettings(): Promise<ParlaySettings> {
  try {
    const r = await fetch(`${CHAT_BASE}/parlay/settings`, { signal: AbortSignal.timeout(3_000) })
    if (r.ok) {
      const parsed = await r.json()
      // Migration: voiceClearPhrase (single string) → voiceClearPhrases[]
      if (typeof parsed.voiceClearPhrase === 'string' && !Array.isArray(parsed.voiceClearPhrases)) {
        parsed.voiceClearPhrases = parsed.voiceClearPhrase.trim() ? [parsed.voiceClearPhrase.trim()] : []
      }
      delete parsed.voiceClearPhrase
      _settings = { ...DEFAULTS, ...parsed }
      _settingsVersion++
    }
  } catch { /* server not ready — use defaults */ }
  return _settings
}

export async function saveSettings(s: ParlaySettings): Promise<void> {
  _settings = s
  _settingsVersion++
  try {
    await fetch(`${CHAT_BASE}/parlay/settings`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(s),
    })
  } catch {}
}
