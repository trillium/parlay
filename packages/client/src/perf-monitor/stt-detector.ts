// ── Web Speech API Detector ───────────────────────────────────────────────────
// Hooks into SpeechRecognition to track when on-device STT is active

export interface SttState {
  active: boolean
  listening: boolean
}

export const sttState: SttState = { active: false, listening: false }

export function hookSpeechRecognition(): void {
  const SpeechRecognition = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition
  if (!SpeechRecognition) return

  const originalConstruct = SpeechRecognition
  const hooked = function (...args: any[]) {
    const instance = new originalConstruct(...args)

    instance.addEventListener('start', () => { sttState.active = true; sttState.listening = true })
    instance.addEventListener('end', () => { sttState.active = false; sttState.listening = false })
    instance.addEventListener('result', () => { sttState.listening = false })  // processing, not listening
    instance.addEventListener('error', () => { sttState.active = false; sttState.listening = false })

    return instance
  }
  hooked.prototype = originalConstruct.prototype
  ;(window as any).SpeechRecognition = hooked
  ;(window as any).webkitSpeechRecognition = hooked
}
