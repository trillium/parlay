import { test, expect, describe, beforeEach } from 'bun:test'
import { parlayInput } from '../../index'
import { FakeEventSource, sleep, STREAM, resetOwnedSse } from './index'

// ── Owned SSE reconnect with exponential backoff ───────────────────────────────

describe('owned EventSource reconnect (exponential backoff)', () => {
  beforeEach(resetOwnedSse)

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

  test('reconnect resumes from the last seen message id (delta, not full replay)', async () => {
    const el = document.createElement('input')
    const unsub = parlayInput(el, {
      server: 'http://localhost:4242',
      device: 'd-resume',
      reconnect: { initialMs: 5, maxMs: 5 },
      EventSource: FakeEventSource as unknown as typeof EventSource,
    })
    try {
      expect(FakeEventSource.instances).toHaveLength(1)
      expect(FakeEventSource.instances[0].url).not.toContain('after=')

      // A message frame advances the resume cursor; input_action does not.
      FakeEventSource.instances[0].emit('message', { id: 'm42', text: 'hi' })
      FakeEventSource.instances[0].emit('input_action', {
        v: 1, streamId: STREAM, seq: 0, baseVersion: 1, actions: [],
      })
      FakeEventSource.instances[0].fireError()
      await sleep(25)

      expect(FakeEventSource.instances).toHaveLength(2)
      expect(FakeEventSource.instances[1].url).toContain('after=m42')

      // A newer message moves the cursor forward for the next reconnect.
      FakeEventSource.instances[1].emit('message', { id: 'm43', text: 'yo' })
      FakeEventSource.instances[1].fireError()
      await sleep(25)
      expect(FakeEventSource.instances).toHaveLength(3)
      expect(FakeEventSource.instances[2].url).toContain('after=m43')
    } finally {
      unsub()
    }
  })

  test('malformed message frames never poison the resume cursor', async () => {
    const el = document.createElement('input')
    const unsub = parlayInput(el, {
      server: 'http://localhost:4242',
      device: 'd-malformed',
      reconnect: { initialMs: 5, maxMs: 5 },
      EventSource: FakeEventSource as unknown as typeof EventSource,
    })
    try {
      // No id, wrong-typed id, and unparseable JSON: cursor stays empty.
      FakeEventSource.instances[0].emit('message', { text: 'no id here' })
      FakeEventSource.instances[0].emit('message', { id: 42 })
      for (const fn of FakeEventSource.instances[0].listeners['message'] ?? []) fn({ data: 'not-json{{' })
      FakeEventSource.instances[0].fireError()
      await sleep(25)
      expect(FakeEventSource.instances).toHaveLength(2)
      expect(FakeEventSource.instances[1].url).not.toContain('after=')
    } finally {
      unsub()
    }
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