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

// Attribute-safe escape: &, <, >, " and ' are all encoded, so esc() output is
// safe inside a double- OR single-quoted attribute as well as in text. That used
// to be false — quotes passed through, and every caller that interpolated esc()
// into an attribute (rich-text's data-lang, tabs' url pills, thread-lavish's
// href) had to hand-roll a `.replace(/"/g, '%22')` after it. Don't add another
// hand-rolled escape: fix esc() here instead. Config/URL/agent text is
// attacker-influenced (WebFetch output, a file an agent Read), so an unescaped
// quote is an HTML-attribute breakout, not a cosmetic issue.
export function esc(s: unknown): string {
  return String(s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;')
}

// Agent colours are server-supplied registry data (the register-agent body is
// stored verbatim and replayed on the agents / agent_register SSE events) and are
// interpolated into `style="..."` attributes in thread.ts. Validate, don't
// escape: an escaped quote is still a CSS injection, and a raw `"` closes the
// attribute before escaping is even relevant. Anything that is not plainly a
// colour token gets the caller's default instead.
const SAFE_COLOR = /^(?:#[0-9a-fA-F]{3,8}|[a-zA-Z][a-zA-Z-]{1,23}|var\(--[a-zA-Z0-9_-]+\)|(?:rgb|rgba|hsl|hsla)\([0-9a-zA-Z.,%/\s-]{1,64}\))$/

export function cssColor(value: unknown, fallback: string): string {
  const v = typeof value === 'string' ? value.trim() : ''
  return SAFE_COLOR.test(v) ? v : fallback
}

// Linkify an ALREADY-ESCAPED string: markdown [label](url) and bare http(s) URLs.
// Escape-first ordering is load-bearing — raw HTML in messages stays inert and
// only these generated anchors survive. http/https only. esc() encodes `"` and
// `'` as `&quot;`/`&#39;`, so the bare-URL match terminates on those entities
// exactly where it used to terminate on the raw quotes: a URL written inside
// prose quotes links without absorbing the closing quote. Any quote that still
// reaches here is %-encoded, so nothing can break out of the href attribute.
export function linkify(escaped: string): string {
  const re = /\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)|(https?:\/\/(?:(?!&quot;|&#39;)[^\s"'])+)/g
  return escaped.replace(re, (_match, label: string | undefined, mdUrl: string | undefined, bareUrl: string | undefined) => {
    const url = (mdUrl ?? bareUrl ?? '').replace(/"/g, '%22')
    return `<a href="${url}" target="_blank" rel="noopener noreferrer">${label ?? bareUrl}</a>`
  })
}

// Locate elements by an ATTRIBUTE VALUE without building a selector from it.
// Ids are interpolated from server data, and `[data-pa-id="${id}"]` was the
// pattern: a quote/bracket in an id turned the lookup into a DIFFERENT selector
// (silently matching the wrong message) or an unparseable one. The DOMException
// did not close the stream — it aborted that one listener frame: for the SSE
// handler the message was never pushed, rendered, or persisted, and
// loadHistory skipped the remaining history entries of that load. Message ids
// come off a JSONL log whose entries are only mostly server-shaped — ids that do
// not parse are ignored on load, not rewritten — so the value is compared
// instead of spliced. Escape in CSS and you still owe the DOM a hand-rolled
// escape; comparing is the whole fix.
export function byAttr(root: ParentNode, attr: string, value: unknown): HTMLElement[] {
  const out: HTMLElement[] = []
  for (const el of root.querySelectorAll<HTMLElement>(`[${attr}]`)) {
    if (el.getAttribute(attr) === String(value)) out.push(el)
  }
  return out
}

export function firstByAttr(root: ParentNode, attr: string, value: unknown): HTMLElement | null {
  const want = String(value)
  for (const el of root.querySelectorAll<HTMLElement>(`[${attr}]`)) {
    if (el.getAttribute(attr) === want) return el
  }
  return null
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
