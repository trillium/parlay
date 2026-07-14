import { PA_VERSION } from './version'
import { CSS_LAYOUT } from './css-layout'
import { CSS_THREAD } from './css-thread'
import { CSS_FEATURES } from './css-features'
import { CSS_SPEECH } from './css-speech'
import { CSS_SETTINGS } from './css-settings'

export const STYLE = CSS_LAYOUT + CSS_THREAD + CSS_FEATURES + CSS_SPEECH + CSS_SETTINGS

// ── HTML template ─────────────────────────────────────────────────────────────

export const DRAWER_HTML = `
  <div id="pa-backdrop"></div>
  <button id="pa-trigger" title="Agent">◈<div id="pa-badge"></div></button>
  <button id="pa-ann-btn" title="Annotate page">✎</button>

  <div id="pa-drawer">
    <div id="pa-version">v${PA_VERSION}</div>
    <audio id="pa-tts-audio"></audio>
    <div id="pa-hdr">
      <div class="pa-dot" id="pa-dot"></div>
      <div id="pa-title">Agent<span id="pa-sub"></span></div>
      <button id="pa-tts-btn" title="Toggle text-to-speech">TTS</button>
      <button id="pa-log-btn" title="Toggle tool activity log">⚡</button>
      <button id="pa-settings-btn-gear" title="Parlay settings">⚙</button>
      <button id="pa-close">✕</button>
    </div>
    <div id="pa-conn-banner"></div>
    <div id="pa-tabs"></div>

    <div id="pa-ann-strip">
      <div class="pa-ann-strip-head">
        <span class="pa-ann-label">QUEUED <span id="pa-ann-count">0</span></span>
        <button id="pa-ann-exit" title="Exit annotate mode (Esc)">Done</button>
        <button id="pa-ann-send">Send to Agent</button>
      </div>
      <div id="pa-ann-hint">Click any element to mark it · <b>Esc</b> or the ✎ button to exit</div>
      <div id="pa-ann-list"></div>
    </div>

    <div id="pa-toollog"></div>
    <div id="pa-thread">
      <div id="pa-empty">
        <div class="pa-empty-dot"></div>
        <div style="color:var(--pa-body);opacity:.5;font-size:13px">Send a message</div>
        <div style="font-family:var(--pa-mono);font-size:11px;opacity:.4">Replies appear instantly.</div>
      </div>
    </div>

    <div id="pa-input-area">
      <button id="pa-jump" title="Jump to latest">↓</button>
      <button id="pa-fab" title="Switch agent">⇄</button>
      <div id="pa-input-row">
        <button id="pa-attach" title="Attach image">📎</button>
        <input type="file" id="pa-attach-file" accept="image/*" style="display:none">
        <textarea id="pa-input" rows="1" placeholder="Message Agent…"></textarea>
        <button id="pa-send">↑</button>
      </div>
      <div id="pa-hint">⌘↵ send</div>
    </div>

    <div id="pa-sheet">
      <div id="pa-sheet-head"><span>Switch agent</span><button id="pa-sheet-close">✕</button></div>
      <div id="pa-sheet-list"></div>
      <div id="pa-sheet-actions">
        <button class="pa-sheet-act" data-proxy="pa-tts-btn">🔊 TTS</button>
        <button class="pa-sheet-act" data-proxy="pa-log-btn">⚡ Tool log</button>
        <button class="pa-sheet-act" data-proxy="pa-settings-btn-gear">⚙ Settings</button>
        <button class="pa-sheet-act" data-proxy="pa-ann-btn">✎ Annotate</button>
      </div>
    </div>
  </div>

  <div id="pa-popup">
    <div id="pa-popup-lbl"></div>
    <textarea id="pa-popup-in" rows="3" placeholder="Comment on this…"></textarea>
    <div id="pa-popup-btns">
      <button class="pa-pb" id="pa-popup-cancel">CANCEL</button>
      <button class="pa-pb" id="pa-popup-add">ADD ↵</button>
    </div>
  </div>
`

// ── DOM injection ─────────────────────────────────────────────────────────────

export function injectDOM() {
  const styleEl = document.createElement('style')
  styleEl.textContent = STYLE
  document.head.appendChild(styleEl)
  document.body.insertAdjacentHTML('beforeend', DRAWER_HTML)
}

export const LAYOUT_STYLE_ID = 'pa-layout-override'

export function setBodyMargin(on: boolean, side: 'left' | 'right' = 'left') {
  let st = document.getElementById(LAYOUT_STYLE_ID) as HTMLStyleElement | null
  if (on) {
    if (!st) {
      st = document.createElement('style')
      st.id = LAYOUT_STYLE_ID
      document.head.appendChild(st)
    }
    if (side === 'right') {
      st.textContent = 'body { margin-right: 380px !important; margin-left: 0 !important; max-width: calc(100vw - 380px) !important; width: auto !important; box-sizing: border-box !important; }'
    } else {
      st.textContent = 'body { margin-left: 380px !important; margin-right: 0 !important; max-width: calc(100vw - 380px) !important; width: auto !important; box-sizing: border-box !important; }'
    }
  } else {
    document.getElementById(LAYOUT_STYLE_ID)?.remove()
  }
}

// ── DOM refs — populated after injectDOM() ────────────────────────────────────

export let backdrop!: HTMLElement
export let trigger!: HTMLElement
export let badge!: HTMLElement
export let drawer!: HTMLElement
export let thread!: HTMLElement
export let emptyEl!: HTMLElement
export let tabsEl!: HTMLElement
export let dot!: HTMLElement
export let sub!: HTMLElement
export let annToggle!: HTMLElement
export let annStrip!: HTMLElement
export let annCount!: HTMLElement
export let annList!: HTMLElement
export let annSend!: HTMLElement
export let popup!: HTMLElement
export let popupLbl!: HTMLElement
export let popupIn!: HTMLTextAreaElement
export let popupOk!: HTMLElement
export let popupCx!: HTMLElement
export let inputEl!: HTMLTextAreaElement
export let sendBtn!: HTMLButtonElement
export let connBanner!: HTMLElement
export let toolLog!: HTMLElement
export let logBtn!: HTMLElement
export let settingsGearBtn!: HTMLElement

export function bindDOMRefs() {
  backdrop   = document.getElementById('pa-backdrop')!
  trigger    = document.getElementById('pa-trigger')!
  badge      = document.getElementById('pa-badge')!
  drawer     = document.getElementById('pa-drawer')!
  thread     = document.getElementById('pa-thread')!
  emptyEl    = document.getElementById('pa-empty')!
  tabsEl     = document.getElementById('pa-tabs')!
  dot        = document.getElementById('pa-dot')!
  sub        = document.getElementById('pa-sub')!
  annToggle  = document.getElementById('pa-ann-btn')!
  annStrip   = document.getElementById('pa-ann-strip')!
  annCount   = document.getElementById('pa-ann-count')!
  annList    = document.getElementById('pa-ann-list')!
  annSend    = document.getElementById('pa-ann-send')!
  popup      = document.getElementById('pa-popup')!
  popupLbl   = document.getElementById('pa-popup-lbl')!
  popupIn    = document.getElementById('pa-popup-in') as HTMLTextAreaElement
  popupOk    = document.getElementById('pa-popup-add')!
  popupCx    = document.getElementById('pa-popup-cancel')!
  inputEl    = document.getElementById('pa-input') as HTMLTextAreaElement
  sendBtn    = document.getElementById('pa-send') as HTMLButtonElement
  connBanner = document.getElementById('pa-conn-banner')!
  toolLog        = document.getElementById('pa-toollog')!
  logBtn         = document.getElementById('pa-log-btn')!
  settingsGearBtn = document.getElementById('pa-settings-btn-gear')!
}
