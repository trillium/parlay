// ── Thread scroll helper ──────────────────────────────────────────────────────
// Separated so thread.ts stays under the 250-line limit (single-concept rule).

import { atBottom } from './state'
import { thread } from './dom'

export function scrollBottom(force?: boolean, instant?: boolean): void {
  if (force || atBottom) {
    // instant bypasses the thread's CSS scroll-behavior:smooth — used for initial
    // history render and tab switches, where an animated scroll is jarring
    if (instant) thread.scrollTo({ top: thread.scrollHeight, behavior: 'instant' as ScrollBehavior })
    else thread.scrollTop = thread.scrollHeight
  }
}
