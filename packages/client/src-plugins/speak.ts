import { setApi, speak, stopSpeak, speakFrom, playPause, initToggle, probeReadiness } from './speak/player'
import { cacheStats, getClip, clipFetches } from './speak/cache'
import { injectCorrectorStyles, openCorrector } from './speak/corrector'
import { setAudioMuted, isAudioMuted } from './speak/audio-mute'

// ── Mute button initialization ────────────────────────────────────────────────
function initMuteButton() {
  const muteBtn = document.getElementById('pa-mute-btn')
  if (!muteBtn) return
  try { if (localStorage.getItem('pa-audio-muted') === '1') { setAudioMuted(true); muteBtn.classList.add('active') } } catch {}
  muteBtn.addEventListener('click', () => {
    const muted = !isAudioMuted()
    setAudioMuted(muted)
    muteBtn.classList.toggle('active', muted)
    try { localStorage.setItem('pa-audio-muted', muted ? '1' : '0') } catch {}
  })
}

// ── speak — Parlay plugin (#19) ──────────────────────────────────────────────
// Owns everything audible: Kokoro-chunked playback with local fallback,
// per-device TTS toggle, replay transport, readiness dots (#20), and the
// pronunciation corrector. Core keeps only the block RENDER structure
// (speech-highlight) and exposes it via window.__parlay.speechUi.

;(window as any).__parlay?.registerPlugin?.({
  id: 'speak',
  version: '1.0.0',
  minPanel: '3.7.0',
  setup(api: any) {
    setApi(api)
    injectCorrectorStyles(api)
    // Readiness dot styling (#20): grey until the WAV exists, green when ready.
    // task-1h47: dots are now `.pa-replay-dot` in a compact `.pa-dots` row, and the
    // active/spoken dot is flagged with `.pa-replay-dot-active` (synced by
    // highlightBlock) rather than derived from a `.pa-block` wrapper.
    api.ui.injectStyle(`
      .pa-replay-dot::before { background: color-mix(in srgb, var(--pa-muted) 35%, transparent) !important; border-color: color-mix(in srgb, var(--pa-muted) 60%, transparent) !important; }
      .pa-replay-dot.ready::before { background: color-mix(in srgb, var(--pa-green) 30%, transparent) !important; border-color: color-mix(in srgb, var(--pa-green) 65%, transparent) !important; }
      .pa-replay-dot.ready:hover::before { background: var(--pa-green) !important; }
      .pa-replay-dot.pa-replay-dot-active::before { background: var(--pa-amber) !important; border-color: var(--pa-amber) !important; }
    `)

    // Global speech hooks — core and thread transport call through these
    const w = window as any
    w.__paSpeak = speak
    w.__paStopSpeak = stopSpeak
    w.__paResetTts = () => {
      stopSpeak()
      if ('speechSynthesis' in window) try { speechSynthesis.cancel() } catch {}
      const au = document.getElementById('pa-tts-audio') as HTMLAudioElement | null
      if (!au) return
      delete (au as any).dataset.unlocked
      au.src = 'data:audio/wav;base64,UklGRi4AAABXQVZFZm10IBAAAAABAAEAQB8AAAB9AAACABAAZGF0YQIAAAAAAA=='
      au.play().then(() => { au.dataset.unlocked = '1' }).catch(() => {})
    }
    w.__paSpeakFrom = speakFrom
    w.__paPlayPause = playPause
    w.__paFlagSpeech = () => {
      w.__parlay.speechUi.flagLastSpoken()   // silent report still lands in the jsonl
      openCorrector(api)                     // …and the corrector expands (#19)
    }
    w.__paTts = { prefetch: getClip, cacheStats, fetches: clipFetches }
    w.__paAudio = { mute: () => setAudioMuted(true), unmute: () => setAudioMuted(false), isMuted: isAudioMuted }

    initToggle()
    initMuteButton()

    // Readiness probes (#20): mark dots green for blocks whose WAV is already
    // cached — on new messages and for bubbles rendered before we loaded.
    api.sse.on('message', (m: any) => {
      if (m.role === 'agent' && m.id && m.text && m.type !== 'system_update') {
        setTimeout(() => probeReadiness(m.id, m.text), 300)   // after render
      }
    })
    setTimeout(() => {
      document.querySelectorAll('.pa-msg.agent[data-pa-id]').forEach(el => {
        const id = (el as HTMLElement).dataset.paId!
        const text = [...el.querySelectorAll('.pa-sb')].map(s => s.textContent ?? '').join('')
        if (text.trim()) void probeReadiness(id, text)
      })
    }, 1200)
  },
})
