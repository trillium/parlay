import { esc, linkify } from './config'
import { highlight } from './syntax'

// ── Code-aware message text (tier 2: monospace + syntax coloring) ─────────────
// Renders ```fenced``` blocks as <pre><code> with token-level highlighting,
// `inline` as <code>, and escapes + linkifies everything else.
// ALL user text is escaped before it hits the DOM (highlight() escapes internally).
// Used for turn/tool system lines so agent code stops rendering as flat prose.
// An unterminated fence (common in truncated turn previews) simply falls through
// to normal escaped text.

export function richText(raw: string): string {
  const s = String(raw ?? '')
  // Split keeping complete ```…``` fences as their own segments.
  return s.split(/(```[\s\S]*?```)/g).map(part => {
    const fence = part.match(/^```([^\n`]*)\r?\n?([\s\S]*?)```$/)
    if (fence) {
      const lang = fence[1].trim()
      const code = highlight(fence[2].replace(/\n$/, ''), lang)
      return `<pre class="pa-code"${lang ? ` data-lang="${esc(lang)}"` : ''}><code>${code}</code></pre>`
    }
    // Non-fenced: pull out `inline code`, escape + linkify the remainder.
    return part.split(/(`[^`\n]+`)/g).map(seg => {
      const inline = seg.match(/^`([^`\n]+)`$/)
      if (inline) return `<code class="pa-code-inline">${esc(inline[1])}</code>`
      return linkify(esc(seg))
    }).join('')
  }).join('')
}
