import { ttsEnabled, ttsVoice, setTtsEnabled, setTtsVoice } from './state'
import { getSettings } from './settings-modal'
import { cacheStats } from './tts-cache'
import { getClip, splitBlocks, clipFetches } from './speech-clips'
import { wrapBlocks, highlightBlock, clearAllSpeechHighlights, noteSpoken, flagLastSpoken, splitBlocksRaw, spansFor, setPlayButton } from './speech-highlight'

// ── Speech output ─────────────────────────────────────────────────────────────
// Sentence-chunked Kokoro pipeline: blocks fetched concurrently, played gapless
// in order — first sound after block 1 only. IndexedDB clip cache makes replays
// instant. Optional hybrid mode speaks block 1 locally while Kokoro renders.
// speechSynthesis remains the wholesale fallback when the daemon is down.

let session = 0          // increments on every speak/stop — cancels in-flight loops
let _resolveCurrent: (() => void) | null = null

function ttsAudio() { return document.getElementById('pa-tts-audio') as HTMLAudioElement | null }

const clearSpeakingHighlight = clearAllSpeechHighlights

function bubbleOf(msgId?: string) {
  return msgId ? document.querySelector(`[data-pa-id="${msgId}"] .pa-bubble`) : null
}

// Prefer render-time spans (#18 block structure); wrap at speak time only for
// bubbles that lack it. Marks the bubble speaking + flips its ▶ to ⏸.
function speakingSpans(msgId: string | undefined, blocks: ReturnType<typeof splitBlocksRaw>): HTMLElement[] | null {
  const existing = spansFor(msgId)
  const spans = existing ?? wrapBlocks(msgId, blocks)
  bubbleOf(msgId)?.classList.add('pa-speaking')
  if (msgId) setPlayButton(msgId, true)
  return spans
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
async function playChunked(text: string, msgId?: string, startIdx = 0): Promise<boolean> {
  const sid = ++session
  const blocks = splitBlocksRaw(text)
  const clips = blocks.map((b, i) => i >= startIdx ? getClip(b.synth) : Promise.resolve(null))
  const first = await clips[startIdx]
  if (sid !== session) return true            // canceled while fetching
  if (!first) return false
  let spans = speakingSpans(msgId, blocks)    // per-sentence highlight spans (#11/#18)
  for (let i = startIdx; i < blocks.length; i++) {
    const clip = await clips[i]
    if (sid !== session) return true
    if (!spans) spans = spansFor(msgId)       // bubble may render just after speak() starts
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
  let spans = speakingSpans(msgId, blocks)    // per-sentence highlight spans (#11/#18)
  for (let i = 0; i < blocks.length; i++) {
    if (sid !== session) return
    if (!spans) spans = spansFor(msgId)       // bubble may render just after speak() starts
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

// Replay dots (#18): play a message from block i onward. Explicit user tap —
// works regardless of the TTS toggle (the tap doubles as the audio unlock).
export function speakFrom(text: string, msgId: string | undefined, startIdx: number) {
  hardStop()
  void playChunked(text, msgId, Math.max(0, startIdx)).then(ok => {
    if (!ok) {   // daemon cold — local voice from that block onward
      const blocks = splitBlocksRaw(text)
      const spans = speakingSpans(msgId, blocks)
      ;(async () => {
        const sid = ++session
        for (let i = Math.max(0, startIdx); i < blocks.length; i++) {
          if (sid !== session) return
          highlightBlock(spans, i)
          noteSpoken(blocks[i].synth, msgId)
          await speakLocalBlock(blocks[i].synth)
        }
        clearSpeakingHighlight()
      })()
    }
  })
}

// ▶/⏸ per reply (#18): stop if this message is playing, else play from start.
let currentPlayMsg: string | undefined
export function playPause(text: string, msgId?: string) {
  const playingThis = currentPlayMsg === msgId && !!document.querySelector(`[data-pa-id="${msgId}"] .pa-bubble.pa-speaking`)
  if (playingThis) { stopSpeak(); currentPlayMsg = undefined; return }
  currentPlayMsg = msgId
  speakFrom(text, msgId, 0)
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
  ;(window as any).__paSpeakFrom = speakFrom
  ;(window as any).__paPlayPause = playPause
  ;(window as any).__paFlagSpeech = flagLastSpoken
  // Ops/debug surface: prefetch + cache stats without playing audio
  ;(window as any).__paTts = { splitBlocks, prefetch: getClip, cacheStats, fetches: clipFetches }

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
