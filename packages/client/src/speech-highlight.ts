import { esc, linkify, CHAT_BASE } from './config'

// ── Per-sentence speaking highlight + mispronunciation flagging ─────────────
// At speak time the bubble's content is re-rendered as one span per playback
// block (concatenating the raw segments reproduces the original text, so
// pre-wrap formatting survives). As each block starts, .pa-speaking-block
// moves to its span; the bubble-level .pa-speaking stays for compat.

export interface RawBlock { synth: string; raw: string }

let lastSpoken: { sentence: string; msgId?: string } | null = null

export function noteSpoken(sentence: string, msgId?: string) {
  lastSpoken = { sentence, msgId }
}

export function wrapBlocks(msgId: string | undefined, blocks: RawBlock[]): HTMLElement[] | null {
  const bubble = msgId ? document.querySelector(`[data-pa-id="${msgId}"] .pa-bubble`) : null
  if (!bubble) return null
  bubble.classList.add('pa-speaking')
  bubble.innerHTML = blocks.map(b => `<span class="pa-sb">${linkify(esc(b.raw))}</span>`).join('')
    + `<button class="pa-flag" title="Report mispronunciation">🚩</button>`
  ;(bubble.querySelector('.pa-flag') as HTMLElement).addEventListener('click', (e) => {
    e.stopPropagation()
    void flagLastSpoken()
  })
  return [...bubble.querySelectorAll('.pa-sb')] as HTMLElement[]
}

export function highlightBlock(spans: HTMLElement[] | null, active: number) {
  spans?.forEach((s, i) => s.classList.toggle('pa-speaking-block', i === active))
}

export function clearAllSpeechHighlights() {
  document.querySelectorAll('.pa-speaking').forEach(el => el.classList.remove('pa-speaking'))
  document.querySelectorAll('.pa-speaking-block').forEach(el => el.classList.remove('pa-speaking-block'))
}

// Report the last-spoken sentence to Pulse → tts-pronunciation-reports.jsonl.
// Fired by the 🚩 on the bubble or the "flag speech" voice command.
export async function flagLastSpoken(): Promise<boolean> {
  if (!lastSpoken) return false
  try {
    const r = await fetch(`${CHAT_BASE}/tts-report`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sentence: lastSpoken.sentence, clipMeta: { source: 'panel', msgId: lastSpoken.msgId ?? null } }),
    })
    return r.ok
  } catch { return false }
}
