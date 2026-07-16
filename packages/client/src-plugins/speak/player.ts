import { getClip, clipKey, cacheHas } from './cache'
import { reportTtsEvent } from './events'

// ── Speak plugin: playback engine ────────────────────────────────────────────
// Talks to core ONLY through window.__parlay.speechUi (block spans, highlight,
// transport buttons) and the plugin api. Owns TTS on/off state (per-device).

const ui = () => (window as any).__parlay.speechUi
let api: any = null
export function setApi(a: any) { api = a }


let ttsEnabled = false
let ttsVoice: SpeechSynthesisVoice | null = null
let session = 0
let _resolveCurrent: (() => void) | null = null
let _resolveLocal: (() => void) | null = null

const ttsAudio = () => document.getElementById('pa-tts-audio') as HTMLAudioElement | null
const bubbleOf = (msgId?: string) => msgId ? document.querySelector(`[data-pa-id="${msgId}"] .pa-bubble`) : null

// Dot readiness (#20): grey until the block's WAV is available, green when ready
export function markReady(msgId: string | undefined, i: number, ready: boolean) {
  if (!msgId) return
  const dot = document.querySelector(`[data-pa-id="${msgId}"] .pa-block[data-bi="${i}"] .pa-dot-btn`)
  dot?.classList.toggle('ready', ready)
}
export async function probeReadiness(msgId: string, text: string) {
  const blocks = ui().splitBlocksRaw(text)
  for (let i = 0; i < blocks.length; i++) {
    if (await cacheHas(clipKey(blocks[i].synth))) markReady(msgId, i, true)
  }
}

function getClipMarked(text: string, msgId: string | undefined, i: number): Promise<Blob | null> {
  return getClip(text).then(c => { if (c) markReady(msgId, i, true); return c })
}

function speakingSpans(msgId: string | undefined, blocks: any[]): HTMLElement[] | null {
  const spans = ui().spansFor(msgId) ?? ui().wrapBlocks(msgId, blocks)
  bubbleOf(msgId)?.classList.add('pa-speaking')
  if (msgId) ui().setPlayButton(msgId, true)
  return spans
}

function playBlob(blob: Blob): Promise<boolean> {
  return new Promise((resolve) => {
    const au = ttsAudio()
    if (!au) { resolve(false); return }
    const url = URL.createObjectURL(blob)
    let settled = false
    const done = (ok: boolean) => { if (!settled) { settled = true; URL.revokeObjectURL(url); _resolveCurrent = null; resolve(ok) } }
    _resolveCurrent = () => done(true)
    au.onended = () => done(true)
    au.onerror = () => done(false)
    au.src = url
    au.play().catch(() => done(false))
  })
}

function speakLocalBlock(text: string): Promise<void> {
  return new Promise((resolve) => {
    if (!('speechSynthesis' in window)) { resolve(); return }
    const utt = new SpeechSynthesisUtterance(text)
    if (ttsVoice) utt.voice = ttsVoice
    utt.rate = 1.05
    let settled = false
    const done = () => { if (!settled) { settled = true; clearTimeout(wd); _resolveLocal = null; resolve() } }
    const wd = setTimeout(done, (text.split(/\s+/).length / 2.5) * 1000 + 2500)
    _resolveLocal = done
    utt.onend = utt.onerror = done
    speechSynthesis.speak(utt)
  })
}

// Chunked Kokoro; per-block local fallback. False = block 1 unavailable.
async function playChunked(text: string, msgId?: string, startIdx = 0): Promise<boolean> {
  const sid = ++session
  const blocks = ui().splitBlocksRaw(text)
  const clips = blocks.map((b: any, i: number) => i >= startIdx ? getClipMarked(b.synth, msgId, i) : Promise.resolve(null))
  const first = await clips[startIdx]
  if (sid !== session) return true
  if (!first) return false
  let spans = speakingSpans(msgId, blocks)
  for (let i = startIdx; i < blocks.length; i++) {
    const clip = await clips[i]
    if (sid !== session) return true
    if (!spans) spans = ui().spansFor(msgId)
    ui().highlightBlock(spans, i)
    ui().noteSpoken(blocks[i].synth, msgId, i)
    reportTtsEvent('block_start', { blockIndex: i, totalBlocks: blocks.length, msgId, source: clip ? 'kokoro' : 'local' })
    if (clip) {
      const ok = await playBlob(clip)
      if (sid !== session) return true
      if (!ok) await speakLocalBlock(blocks[i].synth)
    } else await speakLocalBlock(blocks[i].synth)
    if (sid !== session) return true
    reportTtsEvent('block_end', { blockIndex: i, totalBlocks: blocks.length, msgId })
  }
  ui().clearAllSpeechHighlights()
  reportTtsEvent('session_done', { msgId, totalBlocks: blocks.length })
  return true
}

// Hybrid (#14 policy): per-block at start — ready → Kokoro, else local NOW.
async function playHybrid(text: string, msgId?: string): Promise<void> {
  const sid = ++session
  const blocks = ui().splitBlocksRaw(text)
  const ready: (Blob | null)[] = blocks.map(() => null)
  blocks.forEach((b: any, i: number) => { getClipMarked(b.synth, msgId, i).then(c => { ready[i] = c }) })
  let spans = speakingSpans(msgId, blocks)
  for (let i = 0; i < blocks.length; i++) {
    if (sid !== session) return
    if (!spans) spans = ui().spansFor(msgId)
    ui().highlightBlock(spans, i)
    ui().noteSpoken(blocks[i].synth, msgId, i)
    const clip = ready[i]
    if (clip) {
      const ok = await playBlob(clip)
      if (sid !== session) return
      if (!ok) await speakLocalBlock(blocks[i].synth)
    } else await speakLocalBlock(blocks[i].synth)
    if (sid !== session) return
  }
  ui().clearAllSpeechHighlights()
}

function hardStop() {
  try { if ('speechSynthesis' in window) speechSynthesis.cancel() } catch {}
  const au = ttsAudio()
  if (au) { try { au.pause(); au.currentTime = 0 } catch {} }
  if (_resolveCurrent) _resolveCurrent()
  if (_resolveLocal) _resolveLocal()
  ui().clearAllSpeechHighlights()
}

export function speak(text: string, msgId?: string) {
  if (!ttsEnabled) { if (msgId) void probeReadiness(msgId, text); return }
  hardStop()
  const s = api?.settings.get()
  if (s?.localOnlyVoice) {
    bubbleOf(msgId)?.classList.add('pa-speaking')
    speakLocalBlock(text).then(() => ui().clearAllSpeechHighlights())
    return
  }
  const hybrid = !!s?.hybridVoice
  const run = hybrid ? playHybrid(text, msgId).then(() => true) : playChunked(text, msgId)
  run.then(ok => {
    if (!ok) {
      bubbleOf(msgId)?.classList.add('pa-speaking')
      speakLocalBlock(text).then(() => ui().clearAllSpeechHighlights())
    }
  })
}

export function stopSpeak() { session++; hardStop(); reportTtsEvent('session_stop') }

export function speakFrom(text: string, msgId: string | undefined, startIdx: number) {
  hardStop()
  const s = api?.settings.get()
  if (s?.localOnlyVoice) {
    // Local-only: bypass Kokoro entirely, stay in this call frame for iOS gesture
    const blocks = ui().splitBlocksRaw(text)
    const spans = speakingSpans(msgId, blocks)
    ;(async () => {
      const sid = ++session
      for (let i = Math.max(0, startIdx); i < blocks.length; i++) {
        if (sid !== session) return
        ui().highlightBlock(spans, i)
        ui().noteSpoken(blocks[i].synth, msgId, i)
        await speakLocalBlock(blocks[i].synth)
      }
      ui().clearAllSpeechHighlights()
    })()
    return
  }
  // Pre-unlock speechSynthesis while still in the user-gesture call frame so
  // the local fallback can speak from the async .then() callback on iOS.
  if ('speechSynthesis' in window) {
    const u = new SpeechSynthesisUtterance(' ')
    u.volume = 0
    speechSynthesis.speak(u)
  }
  void playChunked(text, msgId, Math.max(0, startIdx)).then(ok => {
    if (!ok) {
      const blocks = ui().splitBlocksRaw(text)
      const spans = speakingSpans(msgId, blocks)
      ;(async () => {
        const sid = ++session
        for (let i = Math.max(0, startIdx); i < blocks.length; i++) {
          if (sid !== session) return
          ui().highlightBlock(spans, i)
          ui().noteSpoken(blocks[i].synth, msgId, i)
          await speakLocalBlock(blocks[i].synth)
        }
        ui().clearAllSpeechHighlights()
      })()
    }
  })
}

let currentPlayMsg: string | undefined
export function playPause(text: string, msgId?: string) {
  const playingThis = currentPlayMsg === msgId && !!document.querySelector(`[data-pa-id="${msgId}"] .pa-bubble.pa-speaking`)
  if (playingThis) { stopSpeak(); currentPlayMsg = undefined; return }
  currentPlayMsg = msgId
  speakFrom(text, msgId, 0)
}

// ── TTS toggle + voice ───────────────────────────────────────────────────────
const TTS_KEY = 'pa-tts-enabled'

function unlockAudio() {
  const au = ttsAudio()
  if (!au || au.dataset.unlocked) return
  au.src = 'data:audio/wav;base64,UklGRi4AAABXQVZFZm10IBAAAAABAAEAQB8AAAB9AAACABAAZGF0YQIAAAAAAA=='
  au.play().then(() => { au.dataset.unlocked = '1' }).catch(() => {})
}

function initTTSVoice() {
  if (!('speechSynthesis' in window)) return
  const voices = speechSynthesis.getVoices()
  ttsVoice = voices.find(v => v.name === 'Samantha') || voices.find(v => v.name === 'Karen')
    || voices.find(v => v.lang === 'en-US') || voices.find(v => v.lang.startsWith('en')) || voices[0] || null
}

export function initToggle() {
  if ('speechSynthesis' in window) {
    speechSynthesis.addEventListener('voiceschanged', initTTSVoice)
    initTTSVoice()
  }
  const ttsBtn = document.getElementById('pa-tts-btn')
  if (!ttsBtn) return
  try { if (localStorage.getItem(TTS_KEY) === '1') { ttsEnabled = true; ttsBtn.classList.add('active') } } catch {}
  document.addEventListener('touchend', unlockAudio, { once: true, passive: true })
  document.addEventListener('click', unlockAudio, { once: true })
  ttsBtn.addEventListener('click', () => {
    ttsEnabled = !ttsEnabled
    ttsBtn.classList.toggle('active', ttsEnabled)
    try { localStorage.setItem(TTS_KEY, ttsEnabled ? '1' : '0') } catch {}
    if (!ttsEnabled) { stopSpeak(); return }
    unlockAudio()
  })
}
