import { test, expect, describe, beforeEach } from 'bun:test'
import { parlayInput, getDeviceId } from '../../index'
import { harness, sleep, resetOwnedSse } from './index'

// ── Lifecycle + identity ───────────────────────────────────────────────────────

describe('lifecycle + device identity', () => {
  beforeEach(resetOwnedSse)

  test('unsubscribe stops further evals and is idempotent', async () => {
    const h = harness()
    h.unsub()
    h.el.value = 'after'
    h.el.dispatchEvent(new Event('input'))
    await sleep(25)
    expect(h.evalCalls).toHaveLength(0)
    expect(() => h.unsub()).not.toThrow()
  })

  test('getDeviceId persists a stable id in localStorage', () => {
    const first = getDeviceId()
    expect(first).toBeTruthy()
    expect(getDeviceId()).toBe(first)
  })

  test('falls back to textContent for contenteditable elements', () => {
    const div = document.createElement('div')
    div.textContent = 'edited'
    const evalCalls: any[] = []
    const fetchImpl = ((url: string, init?: RequestInit) => {
      if (String(url).endsWith('/api/chat/eval')) evalCalls.push(JSON.parse(init!.body as string))
      return Promise.resolve(new Response('{}'))
    }) as unknown as typeof fetch
    const unsub = parlayInput(div, {
      server: 'http://localhost:4242', settleMs: 0, fetch: fetchImpl,
      subscribe: () => () => {},
    })
    div.dispatchEvent(new Event('input'))
    return sleep(10).then(() => {
      expect(evalCalls[0].text).toBe('edited')
      unsub()
    })
  })
})