// ── Config / constants ───────────────────────────────────────────────────────

export const CHAT_BASE = '/api/chat'

// Detect PWA standalone mode (iOS "Add to Home Screen" launch)
export const IS_STANDALONE = !!(
  (navigator as any).standalone ||
  window.matchMedia('(display-mode: standalone)').matches
)

export const DESKTOP_BP = 960

// Talon voice auto-submit patterns
export const TALON_SUBMIT = /\s+(bravely|gravely|briefly|lap)\s*$/i
export const TALON_CLEAR  = /^change inside in input$/i

// Tool activity log
export const TL_MAX = 200

export const TOOL_ICONS: Record<string, string> = {
  Bash: '⬢', Read: '◎', Write: '✎', Edit: '✏', Agent: '◈',
  Monitor: '◉', Skill: '◇', WebFetch: '⊕', WebSearch: '⊗',
  Workflow: '⬡', Glob: '⊞', Grep: '⊟',
}

// ── Helpers ──────────────────────────────────────────────────────────────────

export function esc(s: unknown): string {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

export function fmtTime(iso: string): string {
  try {
    return new Date(iso).toLocaleTimeString('en-US', {
      hour: '2-digit', minute: '2-digit',
      timeZone: 'America/Los_Angeles', hour12: false,
    })
  } catch { return '' }
}

export function fmtTLTime(iso: string): string {
  try {
    return new Date(iso).toLocaleTimeString('en-US', {
      hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
    })
  } catch { return '' }
}
