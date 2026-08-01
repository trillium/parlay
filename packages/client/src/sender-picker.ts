import { esc } from './config'
import { evalDevice, evalVoice } from './input'
import { bumpInputVersion, currentInputVersion } from './commands'
import type { PickerSender } from './commands/dispatcher/types'
import { PA_VERSION } from './version'
import { getSettings } from './settings-modal'

// ── Voice-driven sender picker (full-screen modal) ──────────────────────────
//
// Frontend half of the iMessage reply tool. Mirrors channel-picker.ts for senders.
// The BACKEND owns all state: whether the picker is open, the ordered list, the
// numbering, and how a spoken utterance resolves. This module only RENDERS the
// perception the backend hands back (openSenderPicker) and FIRES events
// (pickerInput → /eval with mode:"sender-select").

const OVERLAY_ID = 'pa-sender-picker-overlay'
const INPUT_ID = 'pa-sender-picker-input'

// Distinct per-open epoch so the picker's streamId never collides with other streams
const _pickerEpoch: string = (crypto as any).randomUUID?.() ?? `sp-${Date.now()}`
let _pickerSeq = 0

let _settleTimer: ReturnType<typeof setTimeout> | null = null

// ── Open ──────────────────────────────────────────────────────────────────────

export function openSenderPicker(prompt: string, senders: PickerSender[]): void {
  closeSenderPicker()   // idempotent: never stack two overlays

  const overlay = document.createElement('div')
  overlay.id = OVERLAY_ID
  overlay.innerHTML = `
    <div id="pa-sender-picker-panel" role="dialog" aria-modal="true" aria-label="Sender picker">
      <div id="pa-sender-picker-head">
        <div id="pa-sender-picker-prompt">${esc(prompt)}</div>
        <button id="pa-sender-picker-close" type="button" aria-label="Close sender picker">✕</button>
      </div>
      <ol id="pa-sender-picker-list">${renderRows(senders)}</ol>
      <div id="pa-sender-picker-hint" aria-live="polite"></div>
      <input id="${INPUT_ID}" type="text" autocomplete="off" autocapitalize="off"
             autocorrect="off" spellcheck="false"
             placeholder="Say a name, number, or number…" />
      <div id="pa-sender-picker-esc">Tap ✕, tap outside, or say "cancel" to close</div>
    </div>`
  document.body.appendChild(overlay)

  const closeBtn = document.getElementById('pa-sender-picker-close')
  if (closeBtn) closeBtn.addEventListener('click', () => closeSenderPicker())

  const input = document.getElementById(INPUT_ID) as HTMLInputElement | null
  if (input) {
    input.addEventListener('input', () => scheduleSelect(input.value))
    input.addEventListener('keydown', (e: KeyboardEvent) => {
      if (e.key === 'Enter') { e.preventDefault(); flushSelect(input.value) }
    })
    setTimeout(() => input.focus(), 30)
  }

  overlay.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key === 'Escape') { e.preventDefault(); closeSenderPicker() }
  })
  overlay.addEventListener('mousedown', (e: MouseEvent) => {
    if (e.target === overlay) closeSenderPicker()
  })
}

function renderRows(senders: PickerSender[]): string {
  return senders.map(s => {
    const nick = s.nickname && s.nickname !== s.label
      ? `  <span class="pa-sender-picker-nick">(${esc(s.nickname)})</span>` : ''
    return `<li class="pa-sender-picker-row" value="${s.index}"><span class="pa-sender-picker-num">${s.index}.</span> <span class="pa-sender-picker-label">${esc(s.label)}</span>${nick}</li>`
  }).join('')
}

// ── Fire the picker event ─────────────────────────────────────────────────────

function scheduleSelect(text: string): void {
  const { settleMs } = evalVoice()
  if (_settleTimer) clearTimeout(_settleTimer)
  _settleTimer = setTimeout(() => flushSelect(text), Math.max(0, settleMs))
}

function flushSelect(text: string): void {
  if (_settleTimer) { clearTimeout(_settleTimer); _settleTimer = null }
  const t = text.trim()
  if (!t) return
  void postSelect(t)
}

// POST the picker text up with mode:"sender-select" and a DISTINCT streamId.
async function postSelect(text: string): Promise<void> {
  const version = bumpInputVersion()
  const { voiceEnabled } = evalVoice()
  try {
    await fetch('/api/chat/eval', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        streamId: `sender-picker-${evalDevice()}-${_pickerEpoch}`,
        version,
        text,
        mode: 'sender-select',
        cursor: { anchor: 0, active: 0 },
        reason: 'sender-picker',
        voiceEnabled,
        tabs: [],  // no tabs for sender picker
        device: evalDevice(),
        paVersion: PA_VERSION,
      }),
      signal: AbortSignal.timeout(3_000),
    })
    void currentInputVersion()
  } catch { /* network stall — the modal stays open; user can retry */ }
  _pickerSeq++
}

// ── Hint (no match — keep modal open) ─────────────────────────────────────────

export function senderPickerHint(text: string): void {
  const hint = document.getElementById('pa-sender-picker-hint')
  if (!hint) return   // modal already closed — drop silently
  hint.textContent = text
  hint.classList.add('visible')
}

// ── Close ─────────────────────────────────────────────────────────────────────

export function closeSenderPicker(): void {
  if (_settleTimer) { clearTimeout(_settleTimer); _settleTimer = null }
  const overlay = document.getElementById(OVERLAY_ID)
  if (overlay) overlay.remove()
  // Return focus to the main composer
  const main = document.getElementById('pa-input') as HTMLTextAreaElement | null
  if (main && !getSettings().noKeyboardMode) setTimeout(() => main.focus(), 20)
}

export function senderPickerIsOpen(): boolean {
  return !!document.getElementById(OVERLAY_ID)
}

// ── Styles ────────────────────────────────────────────────────────────────────

let _stylesInjected = false
export function injectSenderPickerStyles(): void {
  if (_stylesInjected) return
  _stylesInjected = true
  const s = document.createElement('style')
  s.textContent = `
    #${OVERLAY_ID}{position:fixed;inset:0;z-index:20000;background:rgba(6,10,18,.82);backdrop-filter:blur(3px);display:flex;align-items:center;justify-content:center;padding:24px}
    #pa-sender-picker-panel{width:min(94vw,560px);max-height:88vh;display:flex;flex-direction:column;gap:16px;background:var(--pa-surf,#1e293b);border:1px solid var(--pa-border,#334155);border-radius:14px;padding:26px 24px;box-shadow:0 24px 80px rgba(0,0,0,.6)}
    #pa-sender-picker-head{display:flex;align-items:flex-start;justify-content:space-between;gap:12px}
    #pa-sender-picker-prompt{font-size:16px;font-weight:600;color:var(--pa-body,#e2e8f0);letter-spacing:.01em;line-height:1.35}
    #pa-sender-picker-close{flex:none;width:40px;height:40px;margin:-6px -4px 0 0;border-radius:9px;border:1px solid var(--pa-border,#334155);background:var(--pa-ink,#0f172a);color:var(--pa-muted,#94a3b8);font-size:18px;line-height:1;cursor:pointer;display:flex;align-items:center;justify-content:center}
    #pa-sender-picker-close:hover,#pa-sender-picker-close:active{color:var(--pa-body,#e2e8f0);border-color:var(--pa-red,#f87171)}
    #pa-sender-picker-esc{font-size:12px;color:var(--pa-muted,#94a3b8);text-align:center;font-family:var(--pa-mono,monospace);letter-spacing:.01em}
    #pa-sender-picker-list{list-style:none;margin:0;padding:0;overflow-y:auto;display:flex;flex-direction:column;gap:6px}
    .pa-sender-picker-row{display:flex;align-items:baseline;gap:6px;padding:9px 12px;border-radius:8px;background:color-mix(in srgb,var(--pa-border,#334155) 30%,transparent);font-size:16px;color:var(--pa-body,#e2e8f0);line-height:1.3}
    .pa-sender-picker-num{font-family:var(--pa-mono,monospace);font-weight:700;color:var(--pa-blue,#58A6FF);min-width:1.6em}
    .pa-sender-picker-label{font-weight:600}
    .pa-sender-picker-nick{color:var(--pa-muted,#94a3b8);font-size:14px}
    #pa-sender-picker-hint{display:none;font-size:13px;color:var(--pa-red,#f87171);font-family:var(--pa-mono,monospace);line-height:1.4}
    #pa-sender-picker-hint.visible{display:block}
    #${INPUT_ID}{width:100%;box-sizing:border-box;font-size:18px;padding:14px 16px;border-radius:10px;border:2px solid var(--pa-blue,#58A6FF);background:var(--pa-ink,#0f172a);color:var(--pa-body,#e2e8f0);outline:none;font-family:inherit}
    #${INPUT_ID}::placeholder{color:var(--pa-dim,#64748b)}
    #${INPUT_ID}:focus{border-color:var(--pa-green,#34d399);box-shadow:0 0 0 3px color-mix(in srgb,var(--pa-green,#34d399) 22%,transparent)}
  `
  document.head.appendChild(s)
}
