// Annotate menu — the tiny routing chooser that opens when the annotate button
// is tapped.
//
// Instead of arming annotate mode immediately, tapping the annotate button opens
// a small 3-row popover so the human consciously picks WHERE the annotations
// (and any commands sent from the page) will go before marking anything up:
//   1. "Send commands to active channel"   → setSendTarget('active')  + arm
//   2. "Send commands to enrolled channel" → setSendTarget('enrolled') + arm
//        (shows the enrolled channel's name; disabled when the page declares no
//         pin — window.__paPinChannel — because there is nothing to enroll to)
//   3. "Close"                             → dismiss, arm nothing
//
// This popover IS the on-purpose escape hatch made explicit at annotate time:
// choosing 'active' escapes a page's pin for the session, 'enrolled' honors it.
// The passive footer chip (channel-pin.ts renderPinIndicator) mirrors the choice.
//
// Routing (setSendTarget) lives in channel-pin.ts; ARMING annotate mode lives in
// annotation.ts. To avoid an import cycle, annotation.ts injects its arm callback
// via wireAnnotateMenu() — this module never imports annotation.ts.

import { setSendTarget, getEnrolledChannel, currentSendTarget } from './channel-pin'

const MENU_ID = 'pa-annotate-menu'
const STYLE_ID = 'pa-annotate-menu-style'

// Injected by annotation.ts: turns annotate mode ON after a routing choice.
let _arm: (() => void) | null = null
export function wireAnnotateMenu(arm: () => void): void {
  _arm = arm
}

let _styleInjected = false
function injectStyle(): void {
  if (_styleInjected || document.getElementById(STYLE_ID)) { _styleInjected = true; return }
  _styleInjected = true
  const s = document.createElement('style')
  s.id = STYLE_ID
  // Small mobile-first list. pa- palette via CSS vars with literal fallbacks,
  // same convention as channel-picker / channel-pin styles.
  s.textContent = `
    #${MENU_ID}{position:fixed;z-index:2147483646;min-width:230px;max-width:86vw;
      background:var(--pa-panel,#161b22);color:var(--pa-fg,#e6edf3);
      border:1px solid var(--pa-border,#30363d);border-radius:12px;
      box-shadow:0 8px 30px rgba(0,0,0,.45);padding:6px;font-size:14px;
      display:none;flex-direction:column;gap:2px}
    #${MENU_ID}.visible{display:flex}
    #${MENU_ID} .pa-am-row{display:flex;align-items:center;gap:10px;
      padding:11px 12px;border-radius:8px;cursor:pointer;line-height:1.25;
      background:none;border:0;color:inherit;text-align:left;width:100%;
      font:inherit;min-height:44px}
    #${MENU_ID} .pa-am-row:hover,#${MENU_ID} .pa-am-row:focus-visible{
      background:var(--pa-hover,#21262d);outline:none}
    #${MENU_ID} .pa-am-check{width:16px;flex:0 0 16px;color:var(--pa-green,#3FB950)}
    #${MENU_ID} .pa-am-label{flex:1;min-width:0}
    #${MENU_ID} .pa-am-sub{display:block;font-size:12px;opacity:.6;margin-top:1px}
    #${MENU_ID} .pa-am-row[aria-disabled="true"]{opacity:.4;cursor:default}
    #${MENU_ID} .pa-am-close{color:var(--pa-muted,#8b949e);
      border-top:1px solid var(--pa-border,#30363d);margin-top:2px;border-radius:0 0 8px 8px}
  `
  document.head.appendChild(s)
}

function ensureMenu(): HTMLElement {
  let m = document.getElementById(MENU_ID)
  if (m) return m
  injectStyle()
  m = document.createElement('div')
  m.id = MENU_ID
  m.setAttribute('role', 'menu')
  m.setAttribute('aria-label', 'Where do annotations go?')
  document.body.appendChild(m)
  return m
}

function row(opts: {
  label: string; sub?: string; checked?: boolean; disabled?: boolean;
  cls?: string; onClick?: () => void;
}): HTMLButtonElement {
  const b = document.createElement('button')
  b.className = 'pa-am-row' + (opts.cls ? ' ' + opts.cls : '')
  b.setAttribute('role', 'menuitem')
  if (opts.disabled) b.setAttribute('aria-disabled', 'true')
  const check = document.createElement('span')
  check.className = 'pa-am-check'
  check.textContent = opts.checked ? '✓' : ''
  const label = document.createElement('span')
  label.className = 'pa-am-label'
  label.textContent = opts.label
  if (opts.sub) {
    const sub = document.createElement('span')
    sub.className = 'pa-am-sub'
    sub.textContent = opts.sub
    label.appendChild(sub)
  }
  b.append(check, label)
  if (!opts.disabled && opts.onClick) b.addEventListener('click', opts.onClick)
  return b
}

let _dismiss: ((e: Event) => void) | null = null
let _keydown: ((e: KeyboardEvent) => void) | null = null

export function closeAnnotateMenu(): void {
  const m = document.getElementById(MENU_ID)
  if (m) m.classList.remove('visible')
  if (_dismiss) { document.removeEventListener('pointerdown', _dismiss, true); _dismiss = null }
  if (_keydown) { document.removeEventListener('keydown', _keydown, true); _keydown = null }
}

// Choose a routing target, dismiss, then arm annotate mode.
function choose(mode: 'active' | 'enrolled'): void {
  setSendTarget(mode)
  closeAnnotateMenu()
  _arm?.()
}

export function openAnnotateMenu(anchor?: HTMLElement): void {
  const m = ensureMenu()
  const enrolled = getEnrolledChannel()
  const target = currentSendTarget()
  m.innerHTML = ''
  m.append(
    row({
      label: 'Send commands to active channel',
      checked: target === 'active',
      onClick: () => choose('active'),
    }),
    row({
      label: 'Send commands to enrolled channel',
      sub: enrolled ? enrolled.name : 'no enrolled channel on this page',
      checked: target === 'enrolled' && !!enrolled,
      disabled: !enrolled,
      onClick: enrolled ? () => choose('enrolled') : undefined,
    }),
    row({ label: 'Close', cls: 'pa-am-close', onClick: closeAnnotateMenu }),
  )

  // Position above the anchor when there's room, else below; clamped to viewport.
  m.classList.add('visible')
  const mw = m.offsetWidth, mh = m.offsetHeight
  const W = window.innerWidth, H = window.innerHeight
  let left = W - mw - 12, top = H - mh - 12
  if (anchor) {
    const r = anchor.getBoundingClientRect()
    left = Math.min(r.left, W - mw - 8)
    top = r.top - mh - 8
    if (top < 8) top = Math.min(r.bottom + 8, H - mh - 8)
  }
  m.style.left = Math.max(8, left) + 'px'
  m.style.top = Math.max(8, top) + 'px'

  // Dismiss on outside pointerdown or Escape (capture phase so it wins).
  _dismiss = (e: Event) => { if (!m.contains(e.target as Node)) closeAnnotateMenu() }
  _keydown = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); closeAnnotateMenu() } }
  document.addEventListener('pointerdown', _dismiss, true)
  document.addEventListener('keydown', _keydown, true)
}
