import { esc, linkify, CHAT_BASE } from './config'
import { splitBlocksRaw, linkRanges, type RawBlock } from './speech-segment'

// Re-export the pure segmentation core (unit-tested in speech-segment.test.ts).
export { splitBlocksRaw, linkRanges }
export type { RawBlock }

// ── Per-sentence speaking highlight + mispronunciation flagging ─────────────
// At speak time the bubble's content is re-rendered as one span per playback
// block (concatenating the raw segments reproduces the original text, so
// pre-wrap formatting survives). As each block starts, .pa-speaking-block
// moves to its span; the bubble-level .pa-speaking stays for compat.

// Render-time structure for agent bubbles (task-1h47): the message stays ONE
// paragraph. Passages are INLINE <span class="pa-sb"> inside a single .pa-para
// block — NOT block-level rows — so paragraphs flow and a link that spans two
// passages keeps its anchor whole (splitBlocksRaw won't cut mid-link). Each span
// is tap-to-re-read (rewired in thread.ts to __paSpeakFrom by its data-bi). The
// reading-progress "dots" are kept as a compact row, one per passage, likewise
// tappable to re-read. Play/stop + flag controls follow.
export function blocksHtml(text: string): string {
  if (!text.trim()) return ''
  const blocks = splitBlocksRaw(text)
  const spans = blocks.map((b, i) =>
    `<span class="pa-sb" data-bi="${i}" title="Tap to re-read this passage">${linkify(esc(b.raw))}</span>`
  ).join('')
  const dots = blocks.map((_, i) =>
    `<button class="pa-replay-dot" data-bi="${i}" title="Re-read passage ${i + 1}"></button>`
  ).join('')
  return `<div class="pa-para">${spans}</div>`
    + `<div class="pa-dots">${dots}</div>`
    + `<div class="pa-block-ctl"><button class="pa-playpause" title="Play / stop">▶</button><button class="pa-flag" title="Report mispronunciation">🚩</button></div>`
}

// Spans already rendered for this message (render-time structure)
export function spansFor(msgId?: string): HTMLElement[] | null {
  if (!msgId) return null
  const list = [...document.querySelectorAll(`[data-pa-id="${msgId}"] .pa-sb`)] as HTMLElement[]
  return list.length ? list : null
}

// Reflect play state on the message's ▶/⏸ control
export function setPlayButton(msgId: string | undefined, playing: boolean) {
  document.querySelectorAll('.pa-playpause').forEach(b => { b.textContent = '▶' })
  if (playing && msgId) {
    const b = document.querySelector(`[data-pa-id="${msgId}"] .pa-playpause`)
    if (b) b.textContent = '⏸'
  }
}

let lastSpoken: { sentence: string; msgId?: string; blockIdx?: number } | null = null

export function noteSpoken(sentence: string, msgId?: string, blockIdx?: number) {
  lastSpoken = { sentence, msgId, blockIdx }
}

export function getLastSpoken() { return lastSpoken }

export function wrapBlocks(msgId: string | undefined, blocks: RawBlock[]): HTMLElement[] | null {
  const bubble = msgId ? document.querySelector(`[data-pa-id="${msgId}"] .pa-bubble`) : null
  if (!bubble) return null
  bubble.classList.add('pa-speaking')
  // Fallback path (only when render-time .pa-sb spans are absent). Keep the same
  // one-paragraph inline structure as blocksHtml so highlighting stays consistent.
  bubble.innerHTML = `<div class="pa-para">`
    + blocks.map(b => `<span class="pa-sb">${linkify(esc(b.raw))}</span>`).join('')
    + `</div><button class="pa-flag" title="Report mispronunciation">🚩</button>`
  ;(bubble.querySelector('.pa-flag') as HTMLElement).addEventListener('click', (e) => {
    e.stopPropagation()
    void flagLastSpoken()
  })
  return [...bubble.querySelectorAll('.pa-sb')] as HTMLElement[]
}

export function highlightBlock(spans: HTMLElement[] | null, active: number) {
  spans?.forEach((s, i) => s.classList.toggle('pa-speaking-block', i === active))
  // Sync the progress dots in the same message so the active dot follows the
  // spoken passage (task-1h47: dots kept as reading-progress, decoupled from text).
  const root = spans?.[0]?.closest('[data-pa-id]')
  root?.querySelectorAll('.pa-replay-dot').forEach((d, i) => d.classList.toggle('pa-replay-dot-active', i === active))
}

export function clearAllSpeechHighlights() {
  document.querySelectorAll('.pa-speaking').forEach(el => el.classList.remove('pa-speaking'))
  document.querySelectorAll('.pa-speaking-block').forEach(el => el.classList.remove('pa-speaking-block'))
  setPlayButton(undefined, false)
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
