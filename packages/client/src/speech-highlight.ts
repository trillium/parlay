import { esc, linkify, CHAT_BASE } from './config'

// ── Per-sentence speaking highlight + mispronunciation flagging ─────────────
// At speak time the bubble's content is re-rendered as one span per playback
// block (concatenating the raw segments reproduces the original text, so
// pre-wrap formatting survives). As each block starts, .pa-speaking-block
// moves to its span; the bubble-level .pa-speaking stays for compat.

export interface RawBlock { synth: string; raw: string }

// Split into sentence blocks; merge fragments so blocks are ≥60 chars — small
// enough for fast first synthesis, big enough to keep Kokoro prosody natural.
// Raw segments concatenate back to the original text (pre-wrap rendering).
export function splitBlocksRaw(text: string): RawBlock[] {
  const parts = text.match(/[^.!?\n]+[.!?]*\s*/g) ?? [text]
  const blocks: RawBlock[] = []
  let cur = ''
  for (const p of parts) {
    cur += p
    if (cur.trim().length >= 60) { blocks.push({ synth: cur.trim(), raw: cur }); cur = '' }
  }
  if (cur.trim()) blocks.push({ synth: cur.trim(), raw: cur })
  return blocks.length ? blocks : [{ synth: text.trim(), raw: text }]
}

// Render-time block structure for agent bubbles (#18): one row per playback
// block with a replay dot in the gutter, then play/stop + flag controls.
export function blocksHtml(text: string): string {
  if (!text.trim()) return ''
  const rows = splitBlocksRaw(text).map((b, i) =>
    `<div class="pa-block" data-bi="${i}"><button class="pa-dot-btn" title="Replay from here"></button><span class="pa-sb">${linkify(esc(b.raw))}</span></div>`
  ).join('')
  return rows + `<div class="pa-block-ctl"><button class="pa-playpause" title="Play / stop">▶</button><button class="pa-flag" title="Report mispronunciation">🚩</button></div>`
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
