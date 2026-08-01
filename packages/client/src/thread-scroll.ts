// ── Thread scroll helper ──────────────────────────────────────────────────────
// Separated so thread.ts stays under the 250-line limit (single-concept rule).

import { atBottom } from './state'
import { thread } from './dom'
import { logTrace, logError } from './debug-log'

export function scrollBottom(force?: boolean, instant?: boolean): void {
  const willScroll = force || atBottom
  logTrace('scrollBottom', 'invoked', {
    force, instant, atBottom, willScroll,
    threadFound: !!thread,
    scrollTop: thread?.scrollTop, scrollHeight: thread?.scrollHeight, clientHeight: thread?.clientHeight,
  })
  if (!willScroll) return
  const top = thread.scrollHeight
  // instant bypasses the thread's CSS scroll-behavior:smooth — used for initial
  // history render, tab switches, and jump-to-latest, where an animated scroll
  // is jarring. 'instant' is not supported by every WebKit build (notably some
  // iOS Safari versions throw a TypeError on the enum value) — fall back to a
  // plain scrollTop assignment so the scroll still happens even if the smooth
  // CSS-driven path is unavailable.
  if (instant) {
    try {
      thread.scrollTo({ top, behavior: 'instant' as ScrollBehavior })
    } catch (err) {
      logError('scrollBottom', 'scrollTo instant threw, falling back to scrollTop assign', { err: String(err) })
      thread.scrollTop = top
    }
  } else {
    thread.scrollTop = top
  }
  logTrace('scrollBottom', 'done', { scrollTopAfter: thread.scrollTop, scrollHeightAfter: thread.scrollHeight })
}
