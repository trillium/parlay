// api.ts behavior tests — no DOM, no react: global fetch/EventSource are
// stubbed per test. What is pinned:
//   - response-shape normalization (each endpoint's ?? fallbacks)
//   - fail-soft on non-ok status AND on a thrown fetch (server down must
//     resolve to the empty default, never reject — useFleetStore calls
//     these helpers with no .catch)
//   - openEventStream subscribes/unsubscribes the same handler set and
//     closes the stream on cleanup
import { describe, expect, test, afterEach } from 'bun:test'
import { fetchAgents, fetchHistory, fetchCommands, fetchSubscribers, openEventStream } from './api'

const realFetch = globalThis.fetch
const realES = (globalThis as any).EventSource
afterEach(() => {
  globalThis.fetch = realFetch
  ;(globalThis as any).EventSource = realES
})

function stubFetch(body: unknown, ok = true) {
  globalThis.fetch = (async () =>
    ({ ok, json: async () => body }) as Response) as typeof fetch
}

function stubFetchThrows() {
  globalThis.fetch = (async () => {
    throw new TypeError('fetch failed')
  }) as typeof fetch
}

describe('shape normalization', () => {
  test('fetchAgents: array passes through, non-array becomes []', async () => {
    stubFetch([{ id: 'a1' }])
    expect(await fetchAgents()).toEqual([{ id: 'a1' } as any])
    stubFetch({ nope: true })
    expect(await fetchAgents()).toEqual([])
  })

  test('fetchHistory: accepts both bare-array and {messages} envelopes', async () => {
    stubFetch([{ id: 1 }])
    expect(await fetchHistory()).toEqual([{ id: 1 } as any])
    stubFetch({ messages: [{ id: 2 }] })
    expect(await fetchHistory('chan', 5)).toEqual([{ id: 2 } as any])
    stubFetch({})
    expect(await fetchHistory()).toEqual([])
  })

  test('fetchCommands: missing commands key becomes []', async () => {
    stubFetch({ commands: [{ id: 'c' }] })
    expect(await fetchCommands()).toEqual([{ id: 'c' } as any])
    stubFetch({})
    expect(await fetchCommands()).toEqual([])
  })

  test('fetchSubscribers: nested optional keys all default', async () => {
    stubFetch({ parlay: { clients: 3 }, poll: { channels: [{ channel: 'x' }] } })
    expect(await fetchSubscribers()).toEqual({ clients: 3, channels: [{ channel: 'x' } as any] })
    stubFetch({})
    expect(await fetchSubscribers()).toEqual({ clients: 0, channels: [] })
  })
})

describe('fail-soft', () => {
  test('non-ok status returns the empty default', async () => {
    stubFetch({ anything: true }, false)
    expect(await fetchAgents()).toEqual([])
    expect(await fetchHistory()).toEqual([])
    expect(await fetchCommands()).toEqual([])
    expect(await fetchSubscribers()).toEqual({ clients: 0, channels: [] })
  })

  test('a THROWN fetch resolves to the empty default instead of rejecting', async () => {
    stubFetchThrows()
    expect(await fetchAgents()).toEqual([])
    expect(await fetchHistory()).toEqual([])
    expect(await fetchCommands()).toEqual([])
    expect(await fetchSubscribers()).toEqual({ clients: 0, channels: [] })
  })
})

describe('openEventStream', () => {
  class FakeEventSource {
    static last: FakeEventSource | null = null
    url: string
    closed = false
    listeners = new Map<string, Set<(e: MessageEvent) => void>>()
    constructor(url: string) {
      this.url = url
      FakeEventSource.last = this
    }
    addEventListener(name: string, h: (e: MessageEvent) => void) {
      if (!this.listeners.has(name)) this.listeners.set(name, new Set())
      this.listeners.get(name)!.add(h)
    }
    removeEventListener(name: string, h: (e: MessageEvent) => void) {
      this.listeners.get(name)?.delete(h)
    }
    close() { this.closed = true }
    emit(name: string, data: string) {
      this.listeners.get(name)?.forEach(h => h({ data } as MessageEvent))
    }
  }

  test('parses frames, skips malformed ones, cleanup detaches everything', () => {
    ;(globalThis as any).EventSource = FakeEventSource
    const seen: [string, unknown][] = []
    const stop = openEventStream((type, data) => seen.push([type, data]))
    const es = FakeEventSource.last!

    es.emit('message', JSON.stringify({ id: 7 }))
    es.emit('message', '{not json')          // malformed: skipped, no throw
    es.emit('tool_event', JSON.stringify({ tool: 'Bash' }))
    expect(seen).toEqual([['message', { id: 7 }], ['tool_event', { tool: 'Bash' }]])

    stop()
    expect(es.closed).toBe(true)
    for (const set of es.listeners.values()) expect(set.size).toBe(0)
    es.emit('message', JSON.stringify({ id: 8 }))
    expect(seen.length).toBe(2)
  })
})
