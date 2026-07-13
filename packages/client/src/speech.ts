import { CHAT_BASE } from './config'
import { ttsEnabled, ttsVoice, setTtsEnabled, setTtsVoice } from './state'

// ── Speech output ─────────────────────────────────────────────────────────────
// Kokoro (speak daemon behind Pulse /api/chat/tts) first; browser
// speechSynthesis as fallback. "spoken pause" routes to stopSpeak().

function clearSpeakingHighlight() {
  document.querySelectorAll('.pa-speaking').forEach(el => el.classList.remove('pa-speaking'))
}

function bubbleOf(msgId?: string) {
  return msgId ? document.querySelector(`[data-pa-id="${msgId}"] .pa-bubble`) : null
}

// Server TTS. Returns false when the daemon is unreachable/cold or playback is
// blocked — caller falls back to speechSynthesis. Errors stream back as JSON in
// an audio/wav response, so we sniff the RIFF magic instead of the content type.
async function playServerTTS(text: string, msgId?: string): Promise<boolean> {
  try {
    const r = await fetch(`${CHAT_BASE}/tts`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text }),
    })
    const buf = await r.arrayBuffer()
    const h = new Uint8Array(buf.slice(0, 4))
    if (!(h[0] === 0x52 && h[1] === 0x49 && h[2] === 0x46 && h[3] === 0x46)) return false  // not RIFF
    const au = document.getElementById('pa-tts-audio') as HTMLAudioElement
    const url = URL.createObjectURL(new Blob([buf], { type: 'audio/wav' }))
    const bubble = bubbleOf(msgId)
    au.onplay = () => { if (bubble) bubble.classList.add('pa-speaking') }
    au.onended = au.onerror = () => { clearSpeakingHighlight(); URL.revokeObjectURL(url) }
    au.src = url
    await au.play()
    return true
  } catch { return false }
}

function speakLocal(text: string, msgId?: string) {
  if (!('speechSynthesis' in window)) return
  const utt = new SpeechSynthesisUtterance(text)
  if (ttsVoice) utt.voice = ttsVoice
  utt.rate = 1.05
  const bubble = bubbleOf(msgId)
  if (bubble) {
    utt.onstart = () => bubble.classList.add('pa-speaking')
    utt.onend = utt.onerror = () => bubble.classList.remove('pa-speaking')
  }
  speechSynthesis.speak(utt)
}

export function speak(text: string, msgId?: string) {
  if (!ttsEnabled) return
  try { if ('speechSynthesis' in window) speechSynthesis.cancel() } catch {}
  const au = document.getElementById('pa-tts-audio') as HTMLAudioElement | null
  if (au) { try { au.pause() } catch {} }
  clearSpeakingHighlight()
  // Kokoro first, robotic voice only as fallback
  playServerTTS(text, msgId).then(ok => { if (!ok) speakLocal(text, msgId) })
}

// Hard-stop ALL speech output — voice command "spoken pause" routes here.
export function stopSpeak() {
  try { if ('speechSynthesis' in window) speechSynthesis.cancel() } catch {}
  const au = document.getElementById('pa-tts-audio') as HTMLAudioElement | null
  if (au) { try { au.pause(); au.currentTime = 0 } catch {} }
  clearSpeakingHighlight()
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

export function initSpeech() {
  ;(window as any).__paSpeak = speak
  ;(window as any).__paStopSpeak = stopSpeak

  const ttsBtn = document.getElementById('pa-tts-btn')!
  if (!('speechSynthesis' in window)) {
    // Server TTS still works without speechSynthesis — keep the toggle visible
  } else {
    speechSynthesis.addEventListener('voiceschanged', initTTSVoice)
    initTTSVoice()
  }
  ttsBtn.addEventListener('click', () => {
    setTtsEnabled(!ttsEnabled)
    ttsBtn.classList.toggle('active', ttsEnabled)
    if (!ttsEnabled) { stopSpeak(); return }
    // iOS PWA blocks audio that isn't user-initiated; this tap IS the gesture —
    // unlock the persistent element once so later SSE-triggered plays work.
    const au = document.getElementById('pa-tts-audio') as HTMLAudioElement | null
    if (au && !au.dataset.unlocked) {
      au.src = 'data:audio/wav;base64,UklGRi4AAABXQVZFZm10IBAAAAABAAEAQB8AAAB9AAACABAAZGF0YQIAAAAAAA=='
      au.play().then(() => { au.dataset.unlocked = '1' }).catch(() => {})
    }
  })
}
