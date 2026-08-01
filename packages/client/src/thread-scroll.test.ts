import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, describe, beforeEach, afterEach } from 'bun:test'
import { injectDOM, bindDOMRefs, thread } from './dom'
import { scrollBottom } from './thread-scroll'
import { setAtBottom } from './state'

// ── Regression coverage for the phone-only #pa-jump silent-failure bug ────────
// Some WebKit/iOS Safari builds throw a TypeError on
// `Element.scrollTo({ behavior: 'instant' })` because 'instant' is a
// non-standard ScrollBehavior value. Before the fix, that throw propagated out
// of the jump button's click handler and aborted it silently — the unread
// badge/state cleanup never ran and the thread never scrolled. The fix wraps
// the call in try/catch with a plain `scrollTop` assignment fallback.

function mockScrollHeight(el: HTMLElement, value: number) {
  Object.defineProperty(el, 'scrollHeight', { value, configurable: true })
}

beforeEach(() => {
  document.body.innerHTML = ''
  document.head.innerHTML = ''
  injectDOM()
  bindDOMRefs()
  setAtBottom(true)
})

afterEach(() => {
  document.body.innerHTML = ''
  document.head.innerHTML = ''
})

describe('scrollBottom', () => {
  test('falls back to scrollTop assignment when scrollTo({behavior:"instant"}) throws', () => {
    mockScrollHeight(thread, 5000)
    thread.scrollTop = 0
    thread.scrollTo = () => { throw new TypeError("Failed to execute 'scrollTo': The provided value 'instant' is not a valid enum value") }

    expect(() => scrollBottom(true, true)).not.toThrow()
    expect(thread.scrollTop).toBe(5000)
  })

  test('uses scrollTo directly when it does not throw', () => {
    mockScrollHeight(thread, 3000)
    thread.scrollTop = 0
    let calledWith: any = null
    thread.scrollTo = (opts: any) => { calledWith = opts; thread.scrollTop = opts.top }

    scrollBottom(true, true)

    expect(calledWith).toEqual({ top: 3000, behavior: 'instant' })
    expect(thread.scrollTop).toBe(3000)
  })

  test('does nothing when neither force nor atBottom', () => {
    setAtBottom(false)
    mockScrollHeight(thread, 5000)
    thread.scrollTop = 0
    thread.scrollTo = () => { throw new Error('should not be called') }

    expect(() => scrollBottom(false, true)).not.toThrow()
    expect(thread.scrollTop).toBe(0)
  })
})
