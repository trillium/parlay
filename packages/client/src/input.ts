import { CHAT_BASE } from './config'
import { draftSaveTimer, open, activeChannel, setDraftSaveTimer } from './state'
import { inputEl, sendBtn } from './dom'
import { armCompactTimer } from './sse'
import { getSettings } from './settings-modal'
import { runCommandPass } from './commands'

// ── Auto-resize ───────────────────────────────────────────────────────────────

export function autoResize() {
  inputEl.style.height = 'auto'
  inputEl.style.height = Math.min(inputEl.scrollHeight, 140) + 'px'
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

export async function sendMsg(text: string) {
  if (!text || sendBtn.disabled) return
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
      body: JSON.stringify(toAgent ? { text, toAgent } : { text }),
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

// ── Wire input events ─────────────────────────────────────────────────────────

export function wireInputEvents() {
  inputEl.addEventListener('input', () => { autoResize(); checkTalonSubmit(); scheduleDraftSave() })
  inputEl.addEventListener('keydown', (e: KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault()
      sendMsg(inputEl.value.trim())
    }
  })
  sendBtn.addEventListener('click', () => {
    sendMsg(inputEl.value.trim())
  })
}
