// ── #pa-jump ("Jump to latest") ──────────────────────────────────────────────
// Separated out of init.ts so it stays under the 250-line limit (single-
// concept rule, see thread-scroll.ts). Carries verbose logTrace() calls so a
// phone-only repro of the button doing nothing produces a debug-log trace —
// see debug-log.ts and AGENTS.md.

import { setAtBottom, activeChannel, setUnread, unreadByChannel } from './state'
import { scrollBottom } from './thread-scroll'
import { logTrace } from './debug-log'

const SCROLL_KEY = 'pa-scroll-pct'

export function wireJumpButton(thread: HTMLElement, badge: HTMLElement): void {
  let scrollSaveTimer: ReturnType<typeof setTimeout> | null = null

  const jumpBtn = document.getElementById('pa-jump')!
  logTrace('pa-jump', 'listener bound', { found: !!jumpBtn })
  jumpBtn.addEventListener('click', () => {
    logTrace('pa-jump', 'click fired', {
      scrollTop: thread.scrollTop, scrollHeight: thread.scrollHeight, clientHeight: thread.clientHeight,
    })
    try {
      scrollBottom(true, true)   // instant — no smooth animation on a long catch-up
      setUnread(0)
      badge.classList.remove('visible')
      const ch = activeChannel
      if (ch) {
        unreadByChannel[ch] = 0
        const tabBadge = document.getElementById(`pa-tab-unread-${ch}`)
        if (tabBadge) { tabBadge.textContent = ''; tabBadge.classList.remove('visible') }
      }
    } catch (err) {
      logTrace('pa-jump', 'click handler threw', { err: String(err) })
      throw err
    }
  })

  thread.addEventListener('scroll', () => {
    const atBottomNow = thread.scrollTop + thread.clientHeight >= thread.scrollHeight - 50
    setAtBottom(atBottomNow)
    jumpBtn.classList.toggle('visible', !atBottomNow)
    // Debounced save of scroll position as a ratio
    clearTimeout(scrollSaveTimer!)
    scrollSaveTimer = setTimeout(() => {
      const max = thread.scrollHeight - thread.clientHeight
      if (max > 0) {
        localStorage.setItem(SCROLL_KEY, String(thread.scrollTop / max))
      }
    }, 300)
  })
}

export function restoreScroll(thread: HTMLElement): void {
  const raw = localStorage.getItem(SCROLL_KEY)
  const pct = raw === null ? 1 : parseFloat(raw)
  const max = thread.scrollHeight - thread.clientHeight
  logTrace('restoreScroll', 'invoked', { raw, pct, max })
  if (max <= 0) return
  if (!isFinite(pct) || pct >= 0.97) {
    // was at (near) bottom — settle exactly at the bottom after layout, instantly
    thread.scrollTo({ top: max, behavior: 'instant' as ScrollBehavior })
  } else {
    thread.scrollTo({ top: pct * max, behavior: 'instant' as ScrollBehavior })
  }
}
