// Pure passage segmentation for speech rendering (task-1h47). No DOM, no config
// import — so it is unit-testable in isolation. speech-highlight.ts re-exports
// these and layers the DOM/render logic on top.

export interface RawBlock { synth: string; raw: string }

// Character ranges of links in RAW text (same grammar linkify uses on escaped
// text — escaping never moves where a link starts/ends). Used to keep passage
// boundaries off the middle of an anchor.
export function linkRanges(text: string): { start: number; end: number }[] {
  const re = /\[[^\]]+\]\((?:https?:\/\/[^\s)]+)\)|(?:https?:\/\/[^\s"']+)/g
  const ranges: { start: number; end: number }[] = []
  let m: RegExpExecArray | null
  while ((m = re.exec(text)) !== null) ranges.push({ start: m.index, end: m.index + m[0].length })
  return ranges
}

// A boundary strictly inside a link would split the anchor across two spans.
function insideLink(pos: number, links: { start: number; end: number }[]): boolean {
  return links.some((l) => pos > l.start && pos < l.end)
}

// Split into sentence blocks; merge fragments so blocks are ≥60 chars — small
// enough for fast first synthesis, big enough to keep Kokoro prosody natural.
// Raw segments concatenate back to the original text (pre-wrap rendering).
// A block boundary that would land INSIDE a link is deferred until past the link,
// so a URL spanning two sentences stays whole in the passage it STARTS in
// (task-1h47: never split a link anchor across passages/spans).
export function splitBlocksRaw(text: string): RawBlock[] {
  const parts = text.match(/[^.!?\n]+[.!?]*\s*/g) ?? [text]
  const links = linkRanges(text)
  const blocks: RawBlock[] = []
  let cur = ''
  let offset = 0 // char offset of the start of `cur` within `text`
  for (const p of parts) {
    cur += p
    const boundary = offset + cur.length
    if (cur.trim().length >= 60 && !insideLink(boundary, links)) {
      blocks.push({ synth: cur.trim(), raw: cur })
      offset = boundary
      cur = ''
    }
  }
  if (cur.trim()) blocks.push({ synth: cur.trim(), raw: cur })
  return blocks.length ? blocks : [{ synth: text.trim(), raw: text }]
}
