import { esc } from './config'
import { evalTabs, evalDevice, evalVoice } from './input'
import { bumpInputVersion, currentInputVersion } from './commands'
import type { PickerChannel } from './commands/dispatcher/types'
import { PA_VERSION } from './version'

// ── Voice-driven channel picker (full-screen modal) ───────────────────────────
//
// Frontend half of docs/CHANNEL_PICKER_CONTRACT.md. The BACKEND owns all state:
// whether the picker is open, the ordered list, the numbering, and how a spoken
// utterance resolves. This module only RENDERS the perception the backend hands
// back (openChannelPicker) and FIRES events (pickerInput → /eval with
// mode:"channel-select"). Selection is never decided locally — Escape / backdrop
// only dismiss the local display; the actual switch always round-trips.

const OVERLAY_ID = 'pa-picker-overlay'
const INPUT_ID = 'pa-picker-input'

// Distinct per-open epoch so the picker's streamId never collides with the main
// input's stream in the Go engine (its version counter must start fresh).
const _pickerEpoch: string = (crypto as any).randomUUID?.() ?? `p-${Date.now()}`
let _pickerSeq = 0

let _settleTimer: ReturnType<typeof setTimeout> | null = null

// ── Open ──────────────────────────────────────────────────────────────────────

export function openChannelPicker(prompt: string, channels: PickerChannel[]): void {
  closeChannelPicker()   // idempotent: never stack two overlays

  const overlay = document.createElement('div')
  overlay.id = OVERLAY_ID
  overlay.innerHTML = `
    <div id="pa-picker-panel" role="dialog" aria-modal="true" aria-label="Channel picker">
      <div id="pa-picker-prompt">${esc(prompt)}</div>
      <ol id="pa-picker-list">${renderRows(channels)}</ol>
      <div id="pa-picker-hint" aria-live="polite"></div>
      <input id="${INPUT_ID}" type="text" autocomplete="off" autocapitalize="off"
             autocorrect="off" spellcheck="false"
             placeholder="Say a name, nickname, or number…" />
    </div>`
  document.body.appendChild(overlay)

  const input = document.getElementById(INPUT_ID) as HTMLInputElement | null
  if (input) {
    input.addEventListener('input', () => scheduleSelect(input.value))
    // Enter fires immediately (no settle wait) — an explicit human submit.
    input.addEventListener('keydown', (e: KeyboardEvent) => {
      if (e.key === 'Enter') { e.preventDefault(); flushSelect(input.value) }
    })
    // Focus so voice dictation lands HERE, not the drawer input behind it.
    setTimeout(() => input.focus(), 30)
  }

  // Escape closes locally (display-only; no selection). Attached to the overlay
  // and removed with it, so it can't leak past a close.
  overlay.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key === 'Escape') { e.preventDefault(); closeChannelPicker() }
  })
  // Backdrop tap closes; a tap on the panel does not.
  overlay.addEventListener('mousedown', (e: MouseEvent) => {
    if (e.target === overlay) closeChannelPicker()
  })
}

function renderRows(channels: PickerChannel[]): string {
  return channels.map(c => {
    const nick = c.nickname && c.nickname !== c.label
      ? `  <span class="pa-picker-nick">(${esc(c.nickname)})</span>` : ''
    return `<li class="pa-picker-row" value="${c.index}"><span class="pa-picker-num">${c.index}.</span> <span class="pa-picker-label">${esc(c.label)}</span>${nick}</li>`
  }).join('')
}

// ── Fire the pickerInput event ────────────────────────────────────────────────

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

// POST the picker text up with mode:"channel-select" and a DISTINCT streamId, the
// SAME tabs payload (same order → stable numbers). Actions (switchTab +
// closeChannelPicker, or pickerHint) return over the existing input_action SSE
// channel and flow through the shared dispatcher — no separate apply path.
async function postSelect(text: string): Promise<void> {
  // Bump the global version so the returned envelope's baseVersion is fresh. The
  // picker's three verbs are non-mutating, so they are never dropped as stale
  // regardless of the main input's version — this only keeps bookkeeping honest.
  const version = bumpInputVersion()
  const { voiceEnabled } = evalVoice()
  try {
    await fetch('/api/chat/eval', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        streamId: `picker-${evalDevice()}-${_pickerEpoch}`,
        version,
        text,
        mode: 'channel-select',
        cursor: { anchor: 0, active: 0 },
        reason: 'picker',
        voiceEnabled,
        tabs: evalTabs(),
        device: evalDevice(),
        paVersion: PA_VERSION,
      }),
      signal: AbortSignal.timeout(3_000),
    })
    // The synchronous response is intentionally ignored: the ACTIONS arrive via
    // SSE (single source of truth) so ordering/staleness is uniform with the
    // main input. currentInputVersion() referenced to satisfy the fresh-token
    // contract; the SSE dispatcher does the real work.
    void currentInputVersion()
  } catch { /* network stall — the modal stays open; user can retry */ }
  _pickerSeq++
}

// ── Hint (no match — keep modal open) ─────────────────────────────────────────

export function pickerHint(text: string): void {
  const hint = document.getElementById('pa-picker-hint')
  if (!hint) return   // modal already closed — drop silently
  hint.textContent = text
  hint.classList.add('visible')
}

// ── Close ─────────────────────────────────────────────────────────────────────

export function closeChannelPicker(): void {
  if (_settleTimer) { clearTimeout(_settleTimer); _settleTimer = null }
  const overlay = document.getElementById(OVERLAY_ID)
  if (overlay) overlay.remove()
  // Return focus to the main composer so the next utterance targets the thread.
  const main = document.getElementById('pa-input') as HTMLTextAreaElement | null
  if (main) setTimeout(() => main.focus(), 20)
}

export function pickerIsOpen(): boolean {
  return !!document.getElementById(OVERLAY_ID)
}

// ── Styles ────────────────────────────────────────────────────────────────────

let _stylesInjected = false
export function injectChannelPickerStyles(): void {
  if (_stylesInjected) return
  _stylesInjected = true
  const s = document.createElement('style')
  s.textContent = `
    #${OVERLAY_ID}{position:fixed;inset:0;z-index:20000;background:rgba(6,10,18,.82);backdrop-filter:blur(3px);display:flex;align-items:center;justify-content:center;padding:24px}
    #pa-picker-panel{width:min(94vw,560px);max-height:88vh;display:flex;flex-direction:column;gap:16px;background:var(--pa-surf,#1e293b);border:1px solid var(--pa-border,#334155);border-radius:14px;padding:26px 24px;box-shadow:0 24px 80px rgba(0,0,0,.6)}
    #pa-picker-prompt{font-size:16px;font-weight:600;color:var(--pa-body,#e2e8f0);letter-spacing:.01em;line-height:1.35}
    #pa-picker-list{list-style:none;margin:0;padding:0;overflow-y:auto;display:flex;flex-direction:column;gap:6px}
    .pa-picker-row{display:flex;align-items:baseline;gap:6px;padding:9px 12px;border-radius:8px;background:color-mix(in srgb,var(--pa-border,#334155) 30%,transparent);font-size:16px;color:var(--pa-body,#e2e8f0);line-height:1.3}
    .pa-picker-num{font-family:var(--pa-mono,monospace);font-weight:700;color:var(--pa-accent,#14b8a6);min-width:1.6em}
    .pa-picker-label{font-weight:600}
    .pa-picker-nick{color:var(--pa-muted,#94a3b8);font-size:14px}
    #pa-picker-hint{display:none;font-size:13px;color:var(--pa-red,#f87171);font-family:var(--pa-mono,monospace);line-height:1.4}
    #pa-picker-hint.visible{display:block}
    #${INPUT_ID}{width:100%;box-sizing:border-box;font-size:18px;padding:14px 16px;border-radius:10px;border:2px solid var(--pa-accent,#14b8a6);background:var(--pa-ink,#0f172a);color:var(--pa-body,#e2e8f0);outline:none;font-family:inherit}
    #${INPUT_ID}::placeholder{color:var(--pa-dim,#64748b)}
    #${INPUT_ID}:focus{border-color:var(--pa-green,#34d399);box-shadow:0 0 0 3px color-mix(in srgb,var(--pa-green,#34d399) 22%,transparent)}
  `
  document.head.appendChild(s)
}
