import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, describe, beforeEach, afterEach } from 'bun:test'
import { wireJumpButton, restoreScroll } from './jump-button'
import { injectDOM, bindDOMRefs, thread, badge } from './dom'
import { setAtBottom } from './state'

// ── #pa-jump end-to-end: click actually scrolls and clears unread state ───────
// even on a WebKit build where scrollTo({behavior:'instant'}) throws (see
// thread-scroll.test.ts for the underlying scrollBottom fallback coverage).

function mockScrollHeight(el: HTMLElement, value: number) {
  Object.defineProperty(el, 'scrollHeight', { value, configurable: true })
}

beforeEach(() => {
  document.body.innerHTML = ''
  document.head.innerHTML = ''
  localStorage.clear()
  injectDOM()
  bindDOMRefs()
  setAtBottom(true)
})

afterEach(() => {
  document.body.innerHTML = ''
  document.head.innerHTML = ''
  localStorage.clear()
})

describe('wireJumpButton', () => {
  test('click scrolls the thread to bottom and clears the unread badge even when scrollTo throws', () => {
    mockScrollHeight(thread, 4000)
    thread.scrollTop = 0
    thread.scrollTo = () => { throw new TypeError("'instant' is not a valid enum value") }
    badge.classList.add('visible')

    wireJumpButton(thread, badge)
    const jumpBtn = document.getElementById('pa-jump')!

    expect(() => jumpBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }))).not.toThrow()

    expect(thread.scrollTop).toBe(4000)
    expect(badge.classList.contains('visible')).toBe(false)
  })
})

describe('restoreScroll', () => {
  test('falls back to scrollTop assignment when scrollTo throws', () => {
    mockScrollHeight(thread, 5000)
    thread.scrollTop = 0
    thread.scrollTo = () => { throw new TypeError("'instant' is not a valid enum value") }
    localStorage.setItem('pa-scroll-pct', '1')

    expect(() => restoreScroll(thread)).not.toThrow()
    expect(thread.scrollTop).toBe(5000)
  })
})
