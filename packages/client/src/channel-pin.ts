// Channel pin — mindful page→channel mapping with an on-purpose escape hatch.
//
// THE BUG THIS FIXES. Sends used to route via
//   toAgent = activeChannel ?? __paLavishChannel   (input.ts:sendMsg)
// so whatever chat TAB happened to be active won — an annotation/message from a
// proxied page could silently land on the WRONG agent as the human clicked
// between tabs. There was no deliberate page→channel binding.
//
// THE FIX. A page opts IN to a binding ("enrolls") by declaring
//   window.__paPinChannel = '<agentId>'
// where <agentId> is a channel id known to the client (a key of `agentInfo`).
// When a valid pin is in effect AND the send target is 'enrolled' (the default),
// resolvePinnedChannel() returns it and sends go there regardless of the active
// tab. Precedence in sendMsg becomes:
//   resolvePinnedChannel() ?? activeChannel ?? __paLavishChannel
//
// THE ESCAPE HATCH (on-purpose, never silent). The send target is a conscious
// two-way choice: 'enrolled' (the pinned channel) or 'active' (whatever tab is
// active). The annotate-time menu drives it via setSendTarget(); a standalone
// indicator by the send affordance always shows where a send will land.
//   - 'enrolled' → resolvePinnedChannel() returns the pin (bug-fix behavior)
//   - 'active'   → resolvePinnedChannel() returns undefined, so input.ts falls
//                  back to activeChannel ?? __paLavishChannel (the escape)
//
// STICKY, not per-message. The mode is deliberate module state that persists for
// the session (until the human picks again or the page reloads). The pin exists
// precisely because per-message active-tab drift is the bug; a per-message
// escape would re-introduce that silent drift. One conscious choice, reversible
// any time, with an always-truthful indicator — the human is never surprised.
//
// DEFAULT. When a valid pin exists the default target is 'enrolled' — the page
// enrolled to this channel, so honoring the pin is the safe default and 'active'
// is the explicit escape. With no pin, mode is irrelevant: resolvePinnedChannel()
// is undefined either way and routing is byte-for-byte identical to today.

import { agentInfo, activeChannel } from './state'

export type SendTarget = 'active' | 'enrolled'

// ── Send-target state (sticky, session/module-scoped) ─────────────────────────

// The default is 'enrolled': a page that declares a pin wants its sends to go to
// that channel unless the human deliberately escapes to the active tab.
let _target: SendTarget = 'enrolled'

/** The current conscious send target. Drives the indicator and the menu's
 *  checked row. Note this is the DECLARED mode, independent of whether a valid
 *  pin exists — see effectiveTarget() for the resolved destination. */
export function currentSendTarget(): SendTarget {
  return _target
}

/** Record the human's conscious choice of where sends go, then refresh the
 *  indicator so the passive signal matches the choice immediately. The annotate
 *  menu calls this with 'active' or 'enrolled'. */
export function setSendTarget(mode: SendTarget): void {
  _target = mode
  renderPinIndicator()
}

/** Test-only: restore module state between tests (prod never re-mounts). */
export function _resetChannelPinForTests(): void {
  _target = 'enrolled'
  _styleInjected = false
}

// ── Pin resolution ────────────────────────────────────────────────────────────

// The raw, page-declared pin — read LIVE each call so a page can set it any time
// and so validation runs against the current channel set.
function declaredPin(): string | undefined {
  const raw = (window as any).__paPinChannel
  return typeof raw === 'string' && raw.length > 0 ? raw : undefined
}

// The pin id, honored only if it names a real, known channel. An unknown id is
// treated as "no pin" — we never route to a channel the client can't see.
function validPinId(): string | undefined {
  const pin = declaredPin()
  return pin && agentInfo.has(pin) ? pin : undefined
}

/** The enrolled (pinned) channel resolved to id + display name, or undefined
 *  when no valid pin is declared on this page. The menu shows this on the
 *  "enrolled channel" row; it does NOT depend on the current mode. */
export function getEnrolledChannel(): { id: string; name: string } | undefined {
  const id = validPinId()
  return id ? { id, name: channelLabel(id) } : undefined
}

// Returns the channel a send should be pinned to, or undefined to fall back to
// the existing activeChannel ?? __paLavishChannel routing. Undefined when: the
// target is 'active' (deliberate escape), no pin is declared, or the declared
// pin is not a known channel.
export function resolvePinnedChannel(): string | undefined {
  if (_target !== 'enrolled') return undefined
  return validPinId()
}

// ── Indicator / escape-hatch UI ───────────────────────────────────────────────

const INDICATOR_ID = 'pa-pin-indicator'
const STYLE_ID = 'pa-pin-style'

// Human-facing label for a channel id, matching how tabs.ts titles agents:
// the first nickname, else the registered name, else the raw id.
function channelLabel(id: string): string {
  const info = agentInfo.get(id)
  return info ? (info.nicknames?.[0] ?? info.name) : id
}

// The channel a send will ACTUALLY hit right now, mirroring sendMsg's routing so
// the indicator can never disagree with where the message goes. __paLavishChannel
// is the last-resort fallback (same as input.ts).
function effectiveTarget(): string | undefined {
  return (
    resolvePinnedChannel() ??
    (activeChannel ?? undefined) ??
    ((window as any).__paLavishChannel as string | undefined)
  )
}

// Re-render the indicator to reflect the current effective target and mode.
// Hidden entirely when no valid pin is declared (zero visual change from today
// for un-pinned pages). Exported so the menu/tests can drive re-render directly.
export function renderPinIndicator(): void {
  const el = document.getElementById(INDICATOR_ID)
  if (!el) return
  const enrolled = getEnrolledChannel()
  if (!enrolled) {
    // No pin on this page → nothing to choose between. Keep the element inert and
    // invisible so un-pinned pages are untouched.
    el.className = 'pa-pin-off'
    el.textContent = ''
    el.setAttribute('aria-hidden', 'true')
    return
  }
  el.removeAttribute('aria-hidden')
  const target = effectiveTarget()
  const label = target ? channelLabel(target) : '—'
  const escaped = _target !== 'enrolled'
  el.textContent = `→ ${label}`
  el.classList.remove('pa-pin-off')
  el.classList.add('pa-pin-on')
  el.classList.toggle('pa-pin-escaped', escaped)
  el.title = escaped
    ? `Sending to active tab (${label}). Enrolled channel: ${enrolled.name}.`
    : `Enrolled: sending to ${enrolled.name} regardless of tab.`
  el.setAttribute('aria-label', el.title)
}

// Tapping the standalone indicator is a quick toggle between the two modes — the
// same conscious escape the annotate menu offers, reachable in one tap.
function toggleTarget(): void {
  setSendTarget(_target === 'enrolled' ? 'active' : 'enrolled')
}

let _styleInjected = false
function injectStyle(): void {
  if (_styleInjected || document.getElementById(STYLE_ID)) return
  _styleInjected = true
  const s = document.createElement('style')
  s.id = STYLE_ID
  // Small, mobile-friendly chip in the input footer. Matches the pa- palette
  // (CSS vars with literal fallbacks, same convention as channel-picker styles).
  s.textContent = `
    #${INDICATOR_ID}.pa-pin-off{display:none}
    #${INDICATOR_ID}.pa-pin-on{display:inline-flex;align-items:center;gap:4px;align-self:flex-start;max-width:100%;margin:2px 0 0;padding:3px 9px;border-radius:999px;font-family:var(--pa-mono,monospace);font-size:11px;line-height:1.3;font-weight:600;letter-spacing:.01em;cursor:pointer;user-select:none;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;border:1px solid color-mix(in srgb,var(--pa-green,#34d399) 45%,transparent);background:color-mix(in srgb,var(--pa-green,#34d399) 16%,transparent);color:var(--pa-green,#34d399)}
    #${INDICATOR_ID}.pa-pin-on:hover,#${INDICATOR_ID}.pa-pin-on:active{border-color:var(--pa-green,#34d399)}
    #${INDICATOR_ID}.pa-pin-escaped{border-color:color-mix(in srgb,var(--pa-amber,#fbbf24) 55%,transparent);background:color-mix(in srgb,var(--pa-amber,#fbbf24) 16%,transparent);color:var(--pa-amber,#fbbf24)}
  `
  document.head.appendChild(s)
}

// Mount the indicator once, wire its tap-to-toggle, and paint the initial state.
// The indicator lives in the input footer next to the ⌘↵ hint so it sits right
// by the send affordance. Idempotent — safe if called more than once.
export function wireChannelPin(): void {
  injectStyle()
  let el = document.getElementById(INDICATOR_ID)
  if (!el) {
    el = document.createElement('button')
    el.id = INDICATOR_ID
    el.setAttribute('type', 'button')
    el.className = 'pa-pin-off'
    // Prefer the input footer so the indicator is visually tied to sending; fall
    // back to the input area, then the body, so it always mounts somewhere.
    const inputArea = document.getElementById('pa-input-area')
    const hint = document.getElementById('pa-hint')
    if (inputArea && hint && hint.parentElement === inputArea) {
      inputArea.insertBefore(el, hint)
    } else {
      ;(inputArea ?? document.body).appendChild(el)
    }
    el.addEventListener('click', toggleTarget)
  }
  renderPinIndicator()
}
