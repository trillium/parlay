import { CHAT_BASE } from './config'
import {
  talonTimer, draftSaveTimer, open, activeChannel,
  setTalonTimer, setDraftSaveTimer,
} from './state'
import { inputEl, sendBtn } from './dom'
import { armCompactTimer } from './sse'
import { getSettings } from './settings-modal'

// ── Auto-resize ───────────────────────────────────────────────────────────────

export function autoResize() {
  inputEl.style.height = 'auto'
  inputEl.style.height = Math.min(inputEl.scrollHeight, 140) + 'px'
}

// Expose for draft SSE handler
;(window as any).__paAutoResize = autoResize

// ── Draft sync ────────────────────────────────────────────────────────────────

export function scheduleDraftSave() {
  clearTimeout(draftSaveTimer!)
  setDraftSaveTimer(setTimeout(() => {
    fetch(`${CHAT_BASE}/draft`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text: inputEl.value }),
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
    body: JSON.stringify({ text: '' }),
  }).catch(() => {})
}

// ── Send ──────────────────────────────────────────────────────────────────────

export async function sendMsg(text: string) {
  if (!text || sendBtn.disabled) return
  sendBtn.disabled = true
  inputEl.disabled = true
  try {
    const toAgent = activeChannel ?? ((window as any).__paLavishChannel as string | undefined)
    const r = await fetch(`${CHAT_BASE}/send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(toAgent ? { text, toAgent } : { text }),
    })
    if (r.ok) { inputEl.value = ''; autoResize(); armCompactTimer(); clearDraft() }
  } catch {}
  sendBtn.disabled = false
  inputEl.disabled = false
  inputEl.focus()
}

// ── Talon voice auto-submit ───────────────────────────────────────────────────

function checkTalonSubmit() {
  const s = getSettings()
  if (!s.voiceEnabled) return

  const phrases = s.voiceSubmitPhrases.filter(Boolean)
  const clearPhrase = s.voiceClearPhrase.trim()

  const submitRe = phrases.length
    ? new RegExp(`\\s+(${phrases.map(p => p.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})\\s*$`, 'i')
    : null
  const clearRe = clearPhrase
    ? new RegExp(`^${clearPhrase.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`, 'i')
    : null

  const val = inputEl.value
  if (clearRe && clearRe.test(val.trim())) {
    inputEl.value = ''; autoResize(); return
  }
  if (submitRe && submitRe.test(val)) {
    setTalonTimer(setTimeout(() => {
      if (submitRe.test(inputEl.value)) {
        const stripped = inputEl.value.replace(submitRe, '').trim()
        if (stripped) { inputEl.value = stripped; autoResize(); sendMsg(stripped) }
      }
    }, 1000))
  } else {
    clearTimeout(talonTimer!)
    setTalonTimer(null)
  }
}

// ── Wire input events ─────────────────────────────────────────────────────────

export function wireInputEvents() {
  inputEl.addEventListener('input', () => { autoResize(); checkTalonSubmit(); scheduleDraftSave() })
  inputEl.addEventListener('keydown', (e: KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault()
      clearTimeout(talonTimer!)
      sendMsg(inputEl.value.trim())
    }
  })
  sendBtn.addEventListener('click', () => {
    clearTimeout(talonTimer!)
    sendMsg(inputEl.value.trim())
  })
}
