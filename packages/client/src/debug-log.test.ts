import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, describe, beforeEach, afterEach } from 'bun:test'
import { logTrace, logError } from './debug-log'

// ── Toggle contract (documented in debug-log.ts's header and AGENTS.md) ───────
// localStorage 'pa-debug-log' = '0' must fully disable the shim — no network
// call at all, since the captain may be watching the network tab in
// mobile-console.ts (eruda) while debugging exactly this feature.

describe('debug-log toggle', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    localStorage.clear()
  })

  afterEach(() => {
    localStorage.clear()
    globalThis.fetch = originalFetch
  })

  test('logTrace/logError never call fetch when pa-debug-log is "0"', async () => {
    localStorage.setItem('pa-debug-log', '0')
    let fetchCalled = false
    globalThis.fetch = (() => { fetchCalled = true; throw new Error('fetch should not be called') }) as any

    logTrace('test-source', 'should be dropped')
    logError('test-source', 'should also be dropped')

    // give any (incorrectly) scheduled flush timer a chance to fire
    await new Promise((resolve) => setTimeout(resolve, 10))

    expect(fetchCalled).toBe(false)
  })
})
