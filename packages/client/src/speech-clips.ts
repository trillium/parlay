import { CHAT_BASE } from './config'
import { clipKey, cacheGet, cachePut } from './tts-cache'
import { splitBlocksRaw } from './speech-highlight'

// ── Clip acquisition ─────────────────────────────────────────────────────────
// Cache-first fetch of one block's Kokoro WAV from /api/chat/tts.

let fetchCount = 0
export function clipFetches(): number { return fetchCount }

export function splitBlocks(text: string): string[] {
  return splitBlocksRaw(text).map(b => b.synth)
}

function isRiff(buf: ArrayBuffer): boolean {
  const h = new Uint8Array(buf.slice(0, 4))
  return h[0] === 0x52 && h[1] === 0x49 && h[2] === 0x46 && h[3] === 0x46
}

// null = unavailable (daemon down / error payload) — callers fall back locally.
export async function getClip(text: string): Promise<Blob | null> {
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
