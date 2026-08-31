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

// The panel's interface-capability declaration (docs/interface-capabilities.md):
// exactly the presentation commands this client has live handlers for —
// navigate, reload, draft (sse.ts), input_action (input.ts), device_cmd
// (device-cmd.ts). Sent as ?caps= on the SSE connect; an older server ignores
// the unknown param. Add a name here only with a live handler to back it.
export function panelCapsQuery(instance: string): string {
  const decl = {
    schema: '1.0.0',
    surface: { kind: 'panel', instance },
    accepts: { navigate: {}, reload: {}, device_cmd: {}, input_action: {}, draft: {} },
  }
  return `&caps=${encodeURIComponent(JSON.stringify(decl))}`
}

// ── Helpers ──────────────────────────────────────────────────────────────────

export function esc(s: unknown): string {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// Linkify an ALREADY-ESCAPED string: markdown [label](url) and bare http(s) URLs.
// Escape-first ordering is load-bearing — raw HTML in messages stays inert and
// only these generated anchors survive. http/https only; quotes in URLs are
// %-encoded so they can't break out of the href attribute.
export function linkify(escaped: string): string {
  const re = /\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)|(https?:\/\/[^\s"']+)/g
  return escaped.replace(re, (_match, label: string | undefined, mdUrl: string | undefined, bareUrl: string | undefined) => {
    const url = (mdUrl ?? bareUrl ?? '').replace(/"/g, '%22')
    return `<a href="${url}" target="_blank" rel="noopener noreferrer">${label ?? bareUrl}</a>`
  })
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
