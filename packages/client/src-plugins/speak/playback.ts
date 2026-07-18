import { isAudioMuted } from './audio-mute'

const ttsAudio = () => document.getElementById('pa-tts-audio') as HTMLAudioElement | null
let _resolveCurrent: (() => void) | null = null

export function playBlob(blob: Blob): Promise<boolean> {
  return new Promise((resolve) => {
    if (isAudioMuted()) { resolve(false); return }
    const au = ttsAudio()
    if (!au) { resolve(false); return }
    const url = URL.createObjectURL(blob)
    let settled = false
    const done = (ok: boolean) => {
      if (settled) return
      settled = true
      au.onended = au.onerror = null; au.src = ''
      URL.revokeObjectURL(url); _resolveCurrent = null; resolve(ok)
    }
    _resolveCurrent = () => done(true)
    au.onended = () => done(true)
    au.onerror = () => done(false)
    au.src = url
    au.play().catch(() => done(false))
  })
}

export function stopBlobPlayback() {
  if (_resolveCurrent) _resolveCurrent()
  const au = ttsAudio()
  if (au) { try { au.pause(); au.currentTime = 0 } catch {} }
}
