import { CHAT_BASE } from './config'
import { ttsEnabled, ttsVoice, setTtsEnabled, setTtsVoice } from './state'
import { getSettings } from './settings-modal'
import { clipKey, cacheGet, cachePut, cacheStats } from './tts-cache'
import { wrapBlocks, highlightBlock, clearAllSpeechHighlights, noteSpoken, flagLastSpoken, type RawBlock } from './speech-highlight'

// ── Speech output ─────────────────────────────────────────────────────────────
// Sentence-chunked Kokoro pipeline: blocks fetched concurrently, played gapless
// in order — first sound after block 1 only. IndexedDB clip cache makes replays
// instant. Optional hybrid mode speaks block 1 locally while Kokoro renders.
// speechSynthesis remains the wholesale fallback when the daemon is down.

let session = 0          // increments on every speak/stop — cancels in-flight loops
let fetchCount = 0       // debug: network fetches this page load
let _resolveCurrent: (() => void) | null = null

function ttsAudio() { return document.getElementById('pa-tts-audio') as HTMLAudioElement | null }

const clearSpeakingHighlight = clearAllSpeechHighlights

function bubbleOf(msgId?: string) {
  return msgId ? document.querySelector(`[data-pa-id="${msgId}"] .pa-bubble`) : null
}

// Split into sentence blocks; merge fragments so blocks are ≥60 chars — small
// enough for fast first synthesis, big enough to keep Kokoro prosody natural.
// Raw segments concatenate back to the original text (pre-wrap rendering).
export function splitBlocksRaw(text: string): RawBlock[] {
  const parts = text.match(/[^.!?\n]+[.!?]*\s*/g) ?? [text]
  const blocks: RawBlock[] = []
  let cur = ''
  for (const p of parts) {
    cur += p
    if (cur.trim().length >= 60) { blocks.push({ synth: cur.trim(), raw: cur }); cur = '' }
  }
  if (cur.trim()) blocks.push({ synth: cur.trim(), raw: cur })
  return blocks.length ? blocks : [{ synth: text.trim(), raw: text }]
}

export function splitBlocks(text: string): string[] {
  return splitBlocksRaw(text).map(b => b.synth)
}

function isRiff(buf: ArrayBuffer): boolean {
  const h = new Uint8Array(buf.slice(0, 4))
  return h[0] === 0x52 && h[1] === 0x49 && h[2] === 0x46 && h[3] === 0x46
}

// Cache-first clip fetch. null = unavailable (daemon down / error payload).
async function getClip(text: string): Promise<Blob | null> {
  const key = clipKey(text)
  const hit = await cacheGet(key)
  if (hit) return hit
  try {
    fetchCount++
    const r = await fetch(`${CHAT_BASE}/tts`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text }),
    })
    const buf = await r.arrayBuffer()
    if (!isRiff(buf)) return null
    const blob = new Blob([buf], { type: 'audio/wav' })
    void cachePut(key, blob)
    return blob
  } catch { return null }
}

// Play one clip. Resolves true when playback finished, false when it could
// not play (mobile autoplay rejection, decode error) so callers can fall back
// to the local voice for that block. stopSpeak() unblocks via _resolveCurrent.
function playBlob(blob: Blob): Promise<boolean> {
  return new Promise((resolve) => {
    const au = ttsAudio()
    if (!au) { resolve(false); return }
    const url = URL.createObjectURL(blob)
    let settled = false
    const done = (ok: boolean) => {
      if (settled) return
      settled = true
      URL.revokeObjectURL(url)
      _resolveCurrent = null
      resolve(ok)
    }
    _resolveCurrent = () => done(true)   // stop path; session check exits the loop
    au.onended = () => done(true)
    au.onerror = () => done(false)
    au.src = url
    au.play().catch(() => done(false))  // autoplay policy — never leave unsettled (#14)
  })
}

let _resolveLocal: (() => void) | null = null

// One local utterance. Mobile speechSynthesis is notorious for dropping the
// onend event (#14) — a watchdog at expected-duration+margin resolves anyway
// so a lost event can never stall the rest of the message.
function speakLocalBlock(text: string): Promise<void> {
  return new Promise((resolve) => {
    if (!('speechSynthesis' in window)) { resolve(); return }
    const utt = new SpeechSynthesisUtterance(text)
    if (ttsVoice) utt.voice = ttsVoice
    utt.rate = 1.05
    let settled = false
    const done = () => { if (!settled) { settled = true; clearTimeout(wd); _resolveLocal = null; resolve() } }
    const expectedMs = (text.split(/\s+/).length / 2.5) * 1000 + 2500
    const wd = setTimeout(done, expectedMs)
    _resolveLocal = done
    utt.onend = utt.onerror = done
    speechSynthesis.speak(utt)
  })
}

// Chunked Kokoro playback. Returns false only when block 1 is unavailable and
// nothing played — caller then falls back to speechSynthesis wholesale.
async function playChunked(text: string, msgId?: string): Promise<boolean> {
  const sid = ++session
  const blocks = splitBlocksRaw(text)
  const clips = blocks.map(b => getClip(b.synth))   // concurrent prefetch, ordered playback
  const first = await clips[0]
  if (sid !== session) return true            // canceled while fetching
  if (!first) return false
  const spans = wrapBlocks(msgId, blocks)     // per-sentence highlight spans (#11)
  for (let i = 0; i < blocks.length; i++) {
    const clip = await clips[i]
    if (sid !== session) return true
    highlightBlock(spans, i)
    noteSpoken(blocks[i].synth, msgId)
    if (clip) {
      const ok = await playBlob(clip)
      if (sid !== session) return true
      if (!ok) await speakLocalBlock(blocks[i].synth)   // clip unplayable → local voice (#14)
    } else {
      await speakLocalBlock(blocks[i].synth)            // synth miss → local voice
    }
    if (sid !== session) return true
  }
  clearSpeakingHighlight()
  return true
}

// Hybrid experiment: local voice starts block 1 instantly; hand off to Kokoro
// at the first sentence boundary where its clip is ready. Voices never overlap
// — Kokoro only starts after the current local utterance resolves.
// Hybrid (#14, captain's explicit policy): decide PER BLOCK at its start —
// if that block's Kokoro clip is ready, play it; if not, speak the block with
// the local voice IMMEDIATELY and move on. Never wait for synthesis
// mid-message; the voice may ping-pong between local and Kokoro by design.
async function playHybrid(text: string, msgId?: string): Promise<void> {
  const sid = ++session
  const blocks = splitBlocksRaw(text)
  const ready: (Blob | null)[] = blocks.map(() => null)
  blocks.forEach((b, i) => { getClip(b.synth).then(c => { ready[i] = c }) })   // background fill, never awaited
  const spans = wrapBlocks(msgId, blocks)     // per-sentence highlight spans (#11)
  for (let i = 0; i < blocks.length; i++) {
    if (sid !== session) return
    highlightBlock(spans, i)
    noteSpoken(blocks[i].synth, msgId)
    const clip = ready[i]                     // snapshot at block start — no await
    if (clip) {
      const ok = await playBlob(clip)
      if (sid !== session) return
      if (!ok) await speakLocalBlock(blocks[i].synth)   // unplayable → local, keep moving
    } else {
      await speakLocalBlock(blocks[i].synth)
    }
    if (sid !== session) return
  }
  clearSpeakingHighlight()
}

function hardStop() {
  try { if ('speechSynthesis' in window) speechSynthesis.cancel() } catch {}
  const au = ttsAudio()
  if (au) { try { au.pause(); au.currentTime = 0 } catch {} }
  if (_resolveCurrent) _resolveCurrent()   // unblock a queue awaiting 'ended'
  if (_resolveLocal) _resolveLocal()       // unblock a local utterance whose events got dropped
  clearSpeakingHighlight()
}

export function speak(text: string, msgId?: string) {
  if (!ttsEnabled) return
  hardStop()
  const hybrid = getSettings().hybridVoice
  const run = hybrid ? playHybrid(text, msgId).then(() => true) : playChunked(text, msgId)
  run.then(ok => { if (!ok) { bubbleOf(msgId)?.classList.add('pa-speaking'); speakLocalBlock(text).then(clearSpeakingHighlight) } })
}

// Hard-stop ALL speech output — voice command "spoken pause" routes here.
export function stopSpeak() {
  session++
  hardStop()
}

function initTTSVoice() {
  if (!('speechSynthesis' in window)) return
  const voices = speechSynthesis.getVoices()
  setTtsVoice(
    voices.find(v => v.name === 'Samantha') ||
    voices.find(v => v.name === 'Karen') ||
    voices.find(v => v.lang === 'en-US') ||
    voices.find(v => v.lang.startsWith('en')) ||
    voices[0] || null
  )
}

// PER-DEVICE setting (localStorage): TTS on/off sticks to this device across
// reloads and pushed auto-upgrades. See state.ts for the shared/per-device split.
const TTS_KEY = 'pa-tts-enabled'

function unlockAudio() {
  // iOS PWA blocks audio that isn't user-initiated; any real gesture unlocks
  // the persistent element once, then SSE-triggered plays work for the session.
  const au = ttsAudio()
  if (!au || au.dataset.unlocked) return
  au.src = 'data:audio/wav;base64,UklGRi4AAABXQVZFZm10IBAAAAABAAEAQB8AAAB9AAACABAAZGF0YQIAAAAAAA=='
  au.play().then(() => { au.dataset.unlocked = '1' }).catch(() => {})
}

export function initSpeech() {
  ;(window as any).__paSpeak = speak
  ;(window as any).__paStopSpeak = stopSpeak
  // Ops/debug surface: prefetch + cache stats without playing audio
  ;(window as any).__paTts = { splitBlocks, prefetch: getClip, cacheStats, fetches: () => fetchCount }

  if ('speechSynthesis' in window) {
    speechSynthesis.addEventListener('voiceschanged', initTTSVoice)
    initTTSVoice()
  }
  const ttsBtn = document.getElementById('pa-tts-btn')!
  // Restore this device's TTS preference (bug #10 — it reset on every reload,
  // which the auto-upgrade reloads made constant)
  try {
    if (localStorage.getItem(TTS_KEY) === '1') { setTtsEnabled(true); ttsBtn.classList.add('active') }
  } catch {}
  // Restored-on TTS never saw a toggle tap — unlock on the first real gesture
  document.addEventListener('touchend', unlockAudio, { once: true, passive: true })
  document.addEventListener('click', unlockAudio, { once: true })

  ttsBtn.addEventListener('click', () => {
    setTtsEnabled(!ttsEnabled)
    ttsBtn.classList.toggle('active', ttsEnabled)
    try { localStorage.setItem(TTS_KEY, ttsEnabled ? '1' : '0') } catch {}
    if (!ttsEnabled) { stopSpeak(); return }
    unlockAudio()
  })
}
