import { CHAT_BASE } from './config'
import { ttsEnabled, ttsVoice, setTtsEnabled, setTtsVoice } from './state'
import { getSettings } from './settings-modal'
import { clipKey, cacheGet, cachePut, cacheStats } from './tts-cache'

// ── Speech output ─────────────────────────────────────────────────────────────
// Sentence-chunked Kokoro pipeline: blocks fetched concurrently, played gapless
// in order — first sound after block 1 only. IndexedDB clip cache makes replays
// instant. Optional hybrid mode speaks block 1 locally while Kokoro renders.
// speechSynthesis remains the wholesale fallback when the daemon is down.

let session = 0          // increments on every speak/stop — cancels in-flight loops
let fetchCount = 0       // debug: network fetches this page load
let _resolveCurrent: (() => void) | null = null

function ttsAudio() { return document.getElementById('pa-tts-audio') as HTMLAudioElement | null }

function clearSpeakingHighlight() {
  document.querySelectorAll('.pa-speaking').forEach(el => el.classList.remove('pa-speaking'))
}

function bubbleOf(msgId?: string) {
  return msgId ? document.querySelector(`[data-pa-id="${msgId}"] .pa-bubble`) : null
}

// Split into sentence blocks; merge fragments so blocks are ≥60 chars — small
// enough for fast first synthesis, big enough to keep Kokoro prosody natural.
export function splitBlocks(text: string): string[] {
  const parts = text.match(/[^.!?\n]+[.!?]*\s*/g) ?? [text]
  const blocks: string[] = []
  let cur = ''
  for (const p of parts) {
    cur += p
    if (cur.trim().length >= 60) { blocks.push(cur.trim()); cur = '' }
  }
  if (cur.trim()) blocks.push(cur.trim())
  return blocks.length ? blocks : [text.trim()]
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

// Play one clip; resolves on ended/error/stop. stopSpeak() unblocks via
// _resolveCurrent so a paused element can never hang the queue.
function playBlob(blob: Blob): Promise<void> {
  return new Promise((resolve) => {
    const au = ttsAudio()
    if (!au) { resolve(); return }
    const url = URL.createObjectURL(blob)
    let settled = false
    const done = () => {
      if (settled) return
      settled = true
      URL.revokeObjectURL(url)
      _resolveCurrent = null
      resolve()
    }
    _resolveCurrent = done
    au.onended = done
    au.onerror = done
    au.src = url
    au.play().catch(done)
  })
}

// One local utterance; resolves on end/error (cancel() fires these too).
function speakLocalBlock(text: string): Promise<void> {
  return new Promise((resolve) => {
    if (!('speechSynthesis' in window)) { resolve(); return }
    const utt = new SpeechSynthesisUtterance(text)
    if (ttsVoice) utt.voice = ttsVoice
    utt.rate = 1.05
    utt.onend = utt.onerror = () => resolve()
    speechSynthesis.speak(utt)
  })
}

// Chunked Kokoro playback. Returns false only when block 1 is unavailable and
// nothing played — caller then falls back to speechSynthesis wholesale.
async function playChunked(text: string, msgId?: string): Promise<boolean> {
  const sid = ++session
  const blocks = splitBlocks(text)
  const clips = blocks.map(b => getClip(b))   // concurrent prefetch, ordered playback
  const first = await clips[0]
  if (sid !== session) return true            // canceled while fetching
  if (!first) return false
  bubbleOf(msgId)?.classList.add('pa-speaking')
  for (let i = 0; i < blocks.length; i++) {
    const clip = await clips[i]
    if (sid !== session) return true
    if (!clip) continue                       // mid-stream miss: skip block
    await playBlob(clip)
    if (sid !== session) return true
  }
  clearSpeakingHighlight()
  return true
}

// Hybrid experiment: local voice starts block 1 instantly; hand off to Kokoro
// at the first sentence boundary where its clip is ready. Voices never overlap
// — Kokoro only starts after the current local utterance resolves.
async function playHybrid(text: string, msgId?: string): Promise<void> {
  const sid = ++session
  const blocks = splitBlocks(text)
  const states: ('pending' | 'ready' | 'failed')[] = blocks.map(() => 'pending')
  const clips = blocks.map((b, i) => getClip(b).then(c => { states[i] = c ? 'ready' : 'failed'; return c }))
  bubbleOf(msgId)?.classList.add('pa-speaking')
  let kokoro = false
  for (let i = 0; i < blocks.length; i++) {
    if (sid !== session) return
    if (!kokoro && states[i] === 'ready') kokoro = true
    if (kokoro) {
      const clip = await clips[i]
      if (sid !== session) return
      if (clip) await playBlob(clip)
    } else {
      await speakLocalBlock(blocks[i])
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
