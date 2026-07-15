import { esc } from './config'

// ── Inline images (#17) ───────────────────────────────────────────────────────
// Renders m.images[] plus any image URLs found in the text, for both roles.
// Thumbnails lazy-load, cap at 240px, tap opens full-size via the shared lightbox.

const IMG_URL_RE = /(?:https?:\/\/[^\s"'<>]+|\/[^\s"'<>]+)\.(?:png|jpe?g|gif|webp|svg)(?:\?[^\s"'<>]*)?/gi

export function imagesHtml(m: any): string {
  const fromText: string[] = String(m.text ?? '').match(IMG_URL_RE) ?? []
  const urls = [...new Set([...(Array.isArray(m.images) ? m.images : []), ...fromText])]
    .filter(u => /^(https?:\/\/|\/)/.test(u)).slice(0, 8)
  if (!urls.length) return ''
  const a = (u: string) => esc(u).replace(/"/g, '%22')
  // No anchor: taps open the shared lightbox (delegated in lightbox.ts);
  // long-press/context menu still offers new-tab natively (#17 amendment)
  return `<div class="pa-imgs">${urls.map(u =>
    `<img class="pa-img" loading="lazy" src="${a(u)}" alt="attachment">`
  ).join('')}</div>`
}
