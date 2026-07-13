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

  const phrases = s.voiceSubmitPhrases.filter(Boolean)
  const clearPhrases = (s.voiceClearPhrases ?? []).map(p => p.trim()).filter(Boolean)

  const submitRe = phrases.length
    ? new RegExp(`\\s+(${phrases.map(p => p.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})\\s*$`, 'i')
    : null
  // Clear phrases fire ANYWHERE in the input (spec #8): dictation often lands
  // the command after stray text ("Blah blah change inside input") and the
  // captain wants the WHOLE box emptied. Per-phrase tolerance kept: punctuation
  // or commas between words, dictation-dropped interior words of ≤3 chars.
  // Word-boundary guards stop mid-word hits ("exchange…" is not "change…").
  const SEP = "[\\s,.!?;:]+"
  const clearCores = clearPhrases.map(phrase => {
    const words = phrase.split(/\s+/).map(w => w.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
    return words.map((w, i) => {
      const interior = i > 0 && i < words.length - 1
      if (interior && w.length <= 3) return `(?:${w}${SEP})?`
      return i < words.length - 1 ? `${w}${SEP}` : w
    }).join('')
  })
  const clearRe = clearCores.length
    ? new RegExp(`(?:^|[\\s,.!?;:])(?:${clearCores.join('|')})(?=$|[\\s,.!?;:])`, 'i')
    : null

  const val = inputEl.value

  // Stop phrase ("spoken pause") at the very end of the box → immediately
  // silence current speech, strip the phrase, keep the rest. The input box
  // doubles as a partial command box; this must fire instantly, no timer.
  const stopPhrase = (s.voiceStopPhrase ?? 'spoken pause').trim()
  if (stopPhrase) {
    const stopEsc = stopPhrase.split(/\s+/).map(w => w.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('\\s+')
    const stopRe = new RegExp(`(^|\\s)${stopEsc}[.!?,;]*\\s*$`, 'i')
    if (stopRe.test(val)) {
      if ((window as any).__paStopSpeak) (window as any).__paStopSpeak()
      inputEl.value = val.replace(stopRe, '$1').trimEnd()
      autoResize()
      return
    }
  }

  if (clearRe && clearRe.test(val)) {
    inputEl.value = ''
    autoResize()
    clearDraft()   // cancels the pending debounced save + PUTs empty draft (send-clear hygiene)
    return
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
