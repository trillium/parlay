import { CHAT_BASE } from './config'
import { draftSaveTimer, open, activeChannel, setDraftSaveTimer, agentInfo } from './state'
import { inputEl, sendBtn } from './dom'
import { armCompactTimer } from './sse'
import { getSettings } from './settings-modal'
import { scheduleEval, bumpInputVersion, applyEnvelope } from './commands'
import type { ActionEnvelope } from './commands'
import { wireAttachments, takePendingImages } from './attachments'
import { resolvePinnedChannel } from './channel-pin'
import { switchChannel } from './tabs'
import { sampleInput } from './input-timing'

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

  // ── Local command intercepts (never sent to server) ───────────────────────
  if (!images?.length) {
    const t = text.trim().toLowerCase()

    // Theme toggle: "light mode" / "dark mode" / "toggle theme" / "toggle dark" / "toggle light"
    if (/^(light mode|dark mode|toggle (theme|dark|light|mode))$/.test(t)) {
      const drawer = document.getElementById('pa-drawer')
      if (drawer) {
        const goLight = t === 'light mode' || (t.startsWith('toggle') && !drawer.classList.contains('pa-light'))
        drawer.classList.toggle('pa-light', goLight)
        try { localStorage.setItem('pa-theme', goLight ? 'light' : 'dark') } catch {}
      }
      inputEl.value = ''
      autoResize()
      clearDraft()
      return
    }

    // Channel-switch: bare agent name or @name
    const m = /^@?([\w-]+)$/.exec(text.trim())
    if (m) {
      const candidate = m[1].toLowerCase()
      const exactId = [...agentInfo.keys()].find(id => id.toLowerCase() === candidate)
      const prefixId = !exactId
        ? [...agentInfo.keys()].find(id => id.toLowerCase().startsWith(candidate))
        : undefined
      const target = exactId ?? prefixId
      if (target && target !== activeChannel) {
        inputEl.value = ''
        autoResize()
        clearDraft()
        switchChannel(target)
        return
      }
    }
  }
  // ──────────────────────────────────────────────────────────────────────────

  // Kill any pending debounced draft save FIRST — a full-text draft PUT firing
  // mid-send is what refilled the box after send (mobile race, bug #4).
  clearTimeout(draftSaveTimer!)
  setDraftSaveTimer(null)
  sendBtn.disabled = true
  inputEl.disabled = true
  try {
    // A deliberate page pin (resolvePinnedChannel) wins over active-tab drift so a
    // send from a proxied page can't silently land on whatever tab is active; the
    // pin is opt-in and escapable, so un-pinned pages route exactly as before.
    const toAgent = resolvePinnedChannel() ?? activeChannel ?? ((window as any).__paLavishChannel as string | undefined)
    const r = await fetch(`${CHAT_BASE}/send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text, ...(toAgent ? { toAgent } : {}), ...(images?.length ? { images } : {}) }),
      // A stalled /send must never wedge the composer. On timeout the catch runs
      // and the finally re-enables; the text stays put (only cleared on r.ok).
      signal: AbortSignal.timeout(10_000),
    })
    if (r.ok) { lastSendTs = Date.now(); inputEl.value = ''; autoResize(); armCompactTimer(); clearDraft() }
  } catch {
    // Network error / timeout / abort — leave the text so a failed auto-submit
    // (voice "briefly") can be retried, never silently lost.
  } finally {
    // ALWAYS restore interactivity. Re-enabling lived after the try/catch, not in
    // a finally — so any non-settling /send left the send button greyed
    // (opacity .35, :disabled) and the box unsubmittable forever. Now a stall
    // recovers on its own. This is what wedged the voice auto-submit path once
    // it became the ONLY submit route.
    sendBtn.disabled = false
    inputEl.disabled = false
    inputEl.focus()
  }
}

// ── Server-side eval context ───────────────────────────────────────────────────
// Builds the per-POST context the dispatcher needs: the live tab set (for
// server-side {agent} resolution), the device id, the stream id, the voice gate,
// and the voice-settle tuning knob. Read fresh each schedule so a settings change
// takes effect without a reload.

// Per-page-load epoch: ensures each fresh page gets a new stream ID in the Go
// engine so its version counter starts at 0. Without this, a page refresh resets
// inputVersion to 0 while the engine still has lastVersion=N from the prior
// session, causing every eval to be dropped as stale (engine fast-returns noop
// in <1µs, serverOwnedFires stays 0, voice submit never works).
const _evalPageEpoch: string = (crypto as any).randomUUID?.() ?? `e-${Date.now()}`

// The tab set exactly as the eval wire contract expects it (id, name,
// nicknames). Single source of truth so the main input AND the channel picker
// send an identical, identically-ordered list — the contract requires the
// numbering to stay stable across openChannelPicker and the picker-input POST.
export function evalTabs(): { id: string; name: string; nicknames: string[] }[] {
  return [...agentInfo.values()].map(a => ({ id: a.id, name: a.name, nicknames: a.nicknames ?? [] }))
}

export function evalDevice(): string {
  return (window as any).__paDeviceId as string ?? 'unknown'
}

// Voice gate + settle knob, read fresh so a settings change takes effect with no
// reload. Shared by the main input and the picker.
export function evalVoice(): { voiceEnabled: boolean; settleMs: number } {
  const s = getSettings()
  return {
    voiceEnabled: s.voiceEnabled,
    settleMs: typeof s.voiceSettleMs === 'number' ? s.voiceSettleMs : 450,
  }
}

function evalCtx() {
  const device = evalDevice()
  return {
    ...evalVoice(),
    tabs: evalTabs(),
    device,
    streamId: `eval-${device}-${_evalPageEpoch}`,
  }
}

// ── Wire input events ─────────────────────────────────────────────────────────

export function wireInputEvents() {
  inputEl.addEventListener('input', () => {
    autoResize()
    sampleInput(draftClientId)
    // PURE server-side eval: the client does NO local evaluation. Bump the
    // monotonic input version (the staleness token) and schedule a voice-settle
    // -debounced POST of the STABILIZED buffer to the compiled Go engine.
    bumpInputVersion()
    scheduleEval(() => inputEl.value, evalCtx, false, 'input')
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

// wireServerEval subscribes the action dispatcher to the input_action SSE channel.
// The resync callback re-POSTs the CURRENT buffer so the server recomputes on fresh
// text — the self-correcting loop for stale actions.
export function wireServerEval(onSse: (event: string, handler: (data: any) => void) => void) {
  const resync = (reason: string) => {
    // Bump the version first so the resync POST carries the freshest token, then
    // send immediately (no settle debounce — we already know the text is current).
    bumpInputVersion()
    scheduleEval(() => inputEl.value, evalCtx, true, reason)
  }
  onSse('input_action', (env: ActionEnvelope) => {
    try { applyEnvelope(env, resync) } catch { /* an action must never break input */ }
  })
}

function send() {
  const images = takePendingImages()
  sendMsg(inputEl.value.trim(), images.length ? images : undefined)
}

