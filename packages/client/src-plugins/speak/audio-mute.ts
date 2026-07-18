// Audio mute control — enables/disables all audio playback (network TTS + local speech synthesis)

let audioMuted = false

export function setAudioMuted(muted: boolean) { audioMuted = muted }
export function isAudioMuted(): boolean { return audioMuted }
