import { test, expect, describe, beforeEach } from 'bun:test'
import { parlayInput, getDeviceId } from '../index'
import { harness, sleep, STREAM, FakeEventSource } from './harness'

beforeEach(() => {
  try { localStorage.clear() } catch {}
})

// ── Owned SSE reconnect with exponential backoff ───────────────────────────────

describe('owned EventSource reconnect (exponential backoff)', () => {
  beforeEach(() => { FakeEventSource.instances = [] })

  test('opens /api/chat/events with the device id and wires input_action', () => {
    const el = document.createElement('input')
    const unsub = parlayInput(el, {
      server: 'http://localhost:4242',
      device: 'dev-xyz',
      EventSource: FakeEventSource as unknown as typeof EventSource,
    })
    expect(FakeEventSource.instances).toHaveLength(1)
    const url = FakeEventSource.instances[0].url
    expect(url).toContain('http://localhost:4242/api/chat/events')
    expect(url).toContain('device=dev-xyz')

    // An input_action delivered over the owned stream is applied.
    FakeEventSource.instances[0].emit('input_action', {
      v: 1, streamId: STREAM, seq: 0, baseVersion: 1, actions: [{ verb: 'setText', args: { text: 'live' } }],
    })
    expect(el.value).toBe('live')
    unsub()
  })

  test('backs off exponentially (delay doubles, capped) and resets on open', () => {
    const timers: number[] = []
    const realSetTimeout = globalThis.setTimeout
    // Capture reconnect delays without auto-firing; invoke manually.
    const pending: Array<() => void> = []
    ;(globalThis as any).setTimeout = ((fn: () => void, ms: number) => {
      timers.push(ms)
      pending.push(fn)
      return pending.length as unknown as ReturnType<typeof setTimeout>
    })
    try {
      const el = document.createElement('input')
      const unsub = parlayInput(el, {
        server: 'http://localhost:4242',
        device: 'd',
        reconnect: { initialMs: 10, maxMs: 40 },
        EventSource: FakeEventSource as unknown as typeof EventSource,
      })

      const runLast = () => pending.pop()!()

      // error → schedule 10, delay→20; reconnect builds instance #2
      FakeEventSource.instances[0].fireError()
      runLast()
      // error → schedule 20, delay→40; instance #3
      FakeEventSource.instances[1].fireError()
      runLast()
      // error → schedule 40 (capped), delay stays 40; instance #4
      FakeEventSource.instances[2].fireError()
      runLast()

      expect(timers).toEqual([10, 20, 40])
      expect(FakeEventSource.instances).toHaveLength(4)

      // A successful open resets the backoff to initialMs.
      FakeEventSource.instances[3].fireOpen()
      FakeEventSource.instances[3].fireError()
      expect(timers[timers.length - 1]).toBe(10)

      unsub()
    } finally {
      globalThis.setTimeout = realSetTimeout
    }
  })
})

// ── Lifecycle + identity ───────────────────────────────────────────────────────

describe('lifecycle + device identity', () => {
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
