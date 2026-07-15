import { CHAT_BASE } from './config'
import { draftSaveTimer, open, activeChannel, setDraftSaveTimer } from './state'
import { inputEl, sendBtn } from './dom'
import { armCompactTimer } from './sse'
import { getSettings } from './settings-modal'
import { runCommandPass, scheduleEval, bumpInputVersion, applyEnvelope } from './commands'
import type { ActionEnvelope } from './commands'
import { agentInfo } from './state'
import { wireAttachments, takePendingImages } from './attachments'

// ── Auto-resize ───────────────────────────────────────────────────────────────

// Reflow-guarded (#20): coalesce bursts into one rAF and only write the
// height when it actually changed — the old per-keystroke auto+measure double
// reflow was part of the mobile typing lag. (rAF defers in background tabs;
// resize is purely visual, so that's fine.)
let _resizeQueued = false
let _lastResizeH = 0
export function autoResize() {
  if (_resizeQueued) return
  _resizeQueued = true
  requestAnimationFrame(() => {
    _resizeQueued = false
    inputEl.style.height = 'auto'
    const h = Math.max(38, Math.min(inputEl.scrollHeight, 140))
    if (h !== _lastResizeH) _lastResizeH = h
    inputEl.style.height = h + 'px'
  })
}

// Expose for draft SSE handler
;(window as any).__paAutoResize = autoResize

// ── Draft sync ────────────────────────────────────────────────────────────────

// Per-page-load client id: our own draft PUTs echo back over SSE tagged with
// this id, and sse.ts ignores them — cross-device draft sync stays intact
// while self-echoes can't refill a just-cleared input (mobile send race).
export const draftClientId: string =
  (crypto as any).randomUUID ? crypto.randomUUID() : 'c-' + Math.random().toString(36).slice(2)

// Set on successful send; sse.ts also drops draft events for ~3s after this.
export let lastSendTs = 0

export function scheduleDraftSave() {
  clearTimeout(draftSaveTimer!)
  setDraftSaveTimer(setTimeout(() => {
    fetch(`${CHAT_BASE}/draft`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text: inputEl.value, clientId: draftClientId }),
    }).catch(() => {})
  }, 600))
}

export async function loadDraft() {
  try {
    const r = await fetch(`${CHAT_BASE}/draft`)
    if (!r.ok) return
    const { text } = await r.json()
    if (text && !inputEl.value) { inputEl.value = text; autoResize() }
  } catch {}
}

export function clearDraft() {
  clearTimeout(draftSaveTimer!)
  fetch(`${CHAT_BASE}/draft`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text: '', clientId: draftClientId }),
  }).catch(() => {})
}

// ── Send ──────────────────────────────────────────────────────────────────────

export async function sendMsg(text: string, images?: string[]) {
  if ((!text && !images?.length) || sendBtn.disabled) return
  // Kill any pending debounced draft save FIRST — a full-text draft PUT firing
  // mid-send is what refilled the box after send (mobile race, bug #4).
  clearTimeout(draftSaveTimer!)
  setDraftSaveTimer(null)
  sendBtn.disabled = true
  inputEl.disabled = true
  try {
    const toAgent = activeChannel ?? ((window as any).__paLavishChannel as string | undefined)
    const r = await fetch(`${CHAT_BASE}/send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text, ...(toAgent ? { toAgent } : {}), ...(images?.length ? { images } : {}) }),
    })
    if (r.ok) { lastSendTs = Date.now(); inputEl.value = ''; autoResize(); armCompactTimer(); clearDraft() }
  } catch {}
  sendBtn.disabled = false
  inputEl.disabled = false
  inputEl.focus()
}

// ── Talon voice auto-submit ───────────────────────────────────────────────────

function checkTalonSubmit() {
  const s = getSettings()
  if (!s.voiceEnabled) return
  // Voice/text commands (submit, clear, stop-speech, tab ops, third-party) all
  // live in the command subsystem now — see src/commands/ and COMMANDS.md.
  runCommandPass(inputEl.value)
}

// ── Server-side eval context (feat/server-side-eval) ───────────────────────────
// Builds the per-POST context the dispatcher needs: the live tab set (for
// server-side {agent} resolution), the device id, the stream id, the voice gate,
// and the voice-settle tuning knob. Read fresh each schedule so a settings change
// takes effect without a reload.
function evalCtx() {
  const s = getSettings()
  const device = (window as any).__paDeviceId as string ?? 'unknown'
  return {
    voiceEnabled: s.voiceEnabled,
    settleMs: typeof s.voiceSettleMs === 'number' ? s.voiceSettleMs : 450,
    tabs: [...agentInfo.values()].map(a => ({ id: a.id, name: a.name })),
    device,
    streamId: `eval-${device}-main`,
  }
}

// ── Wire input events ─────────────────────────────────────────────────────────

let _cmdDebounce: ReturnType<typeof setTimeout> | null = null

export function wireInputEvents() {
  inputEl.addEventListener('input', () => {
    autoResize()
    if (getSettings().serverEvalEnabled) {
      // PURE server-side mode: the client does NO local evaluation. Bump the
      // monotonic input version (the staleness token) and schedule a voice-settle
      // -debounced POST of the STABILIZED buffer to the compiled Go engine.
      bumpInputVersion()
      scheduleEval(() => inputEl.value, evalCtx, false, 'input')
    } else {
      // LOCAL mode (flag off) — byte-for-byte today's behavior: command pass
      // debounced 150ms (#20), phrases arrive as dictation bursts.
      clearTimeout(_cmdDebounce!)
      _cmdDebounce = setTimeout(checkTalonSubmit, 150)
    }
    scheduleDraftSave()
  })
  inputEl.addEventListener('keydown', (e: KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault()
      send()
    }
  })
  sendBtn.addEventListener('click', send)
  // Attachments (#17 + addendum): 📎 picker and image paste both queue pending
  // chips above the input; the next send carries them as images[]
  wireAttachments()
}

// wireServerEval subscribes the action dispatcher to the input_action SSE channel
// (feat/server-side-eval). It is always wired but inert unless serverEvalEnabled
// is on, because the server relay returns {disabled:true} and never emits actions
// while the flag is off. The resync callback re-POSTs the CURRENT buffer so the
// server recomputes on fresh text — the self-correcting loop for stale actions.
export function wireServerEval(onSse: (event: string, handler: (data: any) => void) => void) {
  const resync = (reason: string) => {
    // Bump the version first so the resync POST carries the freshest token, then
    // send immediately (no settle debounce — we already know the text is current).
    bumpInputVersion()
    scheduleEval(() => inputEl.value, evalCtx, true, reason)
  }
  onSse('input_action', (env: ActionEnvelope) => {
    if (!getSettings().serverEvalEnabled) return   // flag off — ignore any late events
    try { applyEnvelope(env, resync) } catch { /* an action must never break input */ }
  })
}

function send() {
  const images = takePendingImages()
  sendMsg(inputEl.value.trim(), images.length ? images : undefined)
}
