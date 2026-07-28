import { test, expect, describe, beforeEach, afterEach, mock } from 'bun:test'
import { parlayInput } from './index'

// ── Mock WebSocket ──────────────────────────────────────────────────────────
// happy-dom doesn't implement WebSocket, and we don't want tests hitting a
// real server — so this stands in for the browser's WebSocket for the
// duration of each test, with test-controlled open/message/close.

class MockWebSocket extends EventTarget {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3

  readyState = MockWebSocket.CONNECTING
  url: string
  sent: string[] = []
  static instances: MockWebSocket[] = []

  constructor(url: string) {
    super()
    this.url = url
    MockWebSocket.instances.push(this)
  }

  send(data: string) {
    if (this.readyState !== MockWebSocket.OPEN) throw new Error('socket not open')
    this.sent.push(data)
  }

  close() {
    this.readyState = MockWebSocket.CLOSED
    this.dispatchEvent(new Event('close'))
  }

  // test helpers
  __open() {
    this.readyState = MockWebSocket.OPEN
    this.dispatchEvent(new Event('open'))
  }

  __message(data: unknown) {
    this.dispatchEvent(new MessageEvent('message', { data: JSON.stringify(data) }))
  }
}

let originalWebSocket: unknown
let originalFetch: typeof fetch

beforeEach(() => {
  originalWebSocket = (globalThis as any).WebSocket
  originalFetch = globalThis.fetch
  MockWebSocket.instances = []
  ;(globalThis as any).WebSocket = MockWebSocket
})

afterEach(() => {
  ;(globalThis as any).WebSocket = originalWebSocket
  globalThis.fetch = originalFetch
})

function lastSocket(): MockWebSocket {
  const ws = MockWebSocket.instances[MockWebSocket.instances.length - 1]
  if (!ws) throw new Error('no MockWebSocket was constructed')
  return ws
}

describe('parlayInput — websocket transport (default)', () => {
  test('opens a socket against server + path, translating http -> ws', () => {
    const input = document.createElement('input')
    const unsubscribe = parlayInput(input, { server: 'http://localhost:4242' })
    expect(lastSocket().url).toBe('ws://localhost:4242/api/input')
    unsubscribe()
  })

  test('translates https -> wss', () => {
    const input = document.createElement('input')
    const unsubscribe = parlayInput(input, { server: 'https://example.com' })
    expect(lastSocket().url).toBe('wss://example.com/api/input')
    unsubscribe()
  })

  test('sends the element value on the configured event once the socket is open', () => {
    const input = document.createElement('input')
    const unsubscribe = parlayInput(input, { server: 'http://localhost:4242' })
    const ws = lastSocket()
    ws.__open()

    input.value = 'hello'
    input.dispatchEvent(new Event('input'))

    expect(ws.sent).toHaveLength(1)
    expect(JSON.parse(ws.sent[0])).toEqual({ type: 'input', event: 'input', value: 'hello' })
    unsubscribe()
  })

  test('defers sends until the socket opens, then flushes', () => {
    const input = document.createElement('input')
    const unsubscribe = parlayInput(input, { server: 'http://localhost:4242' })
    const ws = lastSocket()

    input.value = 'queued'
    input.dispatchEvent(new Event('input'))
    expect(ws.sent).toHaveLength(0) // not open yet

    ws.__open()
    expect(ws.sent).toHaveLength(1)
    expect(JSON.parse(ws.sent[0]).value).toBe('queued')
    unsubscribe()
  })

  test('respects a custom event name', () => {
    const input = document.createElement('input')
    const unsubscribe = parlayInput(input, { server: 'http://localhost:4242', event: 'change' })
    const ws = lastSocket()
    ws.__open()

    input.value = 'committed'
    input.dispatchEvent(new Event('change'))
    input.dispatchEvent(new Event('input')) // should NOT trigger a send

    expect(ws.sent).toHaveLength(1)
    expect(JSON.parse(ws.sent[0]).event).toBe('change')
    unsubscribe()
  })

  test('delivers server-pushed actions to the action callback', () => {
    const input = document.createElement('input')
    const received: unknown[] = []
    const unsubscribe = parlayInput(input, {
      server: 'http://localhost:4242',
      action: (a) => received.push(a),
    })
    const ws = lastSocket()
    ws.__open()
    ws.__message({ type: 'clear' })
    ws.__message({ type: 'set-value', payload: 'from server' })

    expect(received).toEqual([{ type: 'clear' }, { type: 'set-value', payload: 'from server' }])
    unsubscribe()
  })

  test('ignores malformed server frames instead of throwing', () => {
    const input = document.createElement('input')
    const received: unknown[] = []
    const unsubscribe = parlayInput(input, {
      server: 'http://localhost:4242',
      action: (a) => received.push(a),
    })
    const ws = lastSocket()
    ws.__open()
    expect(() => ws.dispatchEvent(new MessageEvent('message', { data: 'not json' }))).not.toThrow()

    expect(received).toEqual([])
    unsubscribe()
  })

  test('unsubscribe removes the DOM listener and closes the socket', () => {
    const input = document.createElement('input')
    const unsubscribe = parlayInput(input, { server: 'http://localhost:4242' })
    const ws = lastSocket()
    ws.__open()

    unsubscribe()
    expect(ws.readyState).toBe(MockWebSocket.CLOSED)

    input.value = 'after unsubscribe'
    input.dispatchEvent(new Event('input'))
    expect(ws.sent).toHaveLength(0)
  })

  test('unsubscribe is idempotent', () => {
    const input = document.createElement('input')
    const unsubscribe = parlayInput(input, { server: 'http://localhost:4242' })
    expect(() => {
      unsubscribe()
      unsubscribe()
    }).not.toThrow()
  })
})

describe('parlayInput — post transport', () => {
  test('POSTs the value to server + path as JSON', async () => {
    const calls: Array<{ url: string; init: RequestInit }> = []
    globalThis.fetch = mock((url: string, init: RequestInit) => {
      calls.push({ url: String(url), init })
      return Promise.resolve(new Response('{}'))
    }) as unknown as typeof fetch

    const input = document.createElement('input')
    const unsubscribe = parlayInput(input, { server: 'http://localhost:4242', transport: 'post' })

    input.value = 'posted'
    input.dispatchEvent(new Event('input'))
    await Promise.resolve()

    expect(calls).toHaveLength(1)
    expect(calls[0].url).toBe('http://localhost:4242/api/input')
    expect(calls[0].init.method).toBe('POST')
    expect(JSON.parse(calls[0].init.body as string)).toEqual({ event: 'input', value: 'posted' })
    expect(MockWebSocket.instances).toHaveLength(0) // post mode never opens a socket
    unsubscribe()
  })
})

describe('parlayInput — element value extraction', () => {
  test('falls back to textContent for non-value elements (e.g. contenteditable div)', () => {
    const div = document.createElement('div')
    div.textContent = 'edited text'
    const unsubscribe = parlayInput(div, { server: 'http://localhost:4242' })
    const ws = lastSocket()
    ws.__open()

    div.dispatchEvent(new Event('input'))
    expect(JSON.parse(ws.sent[0]).value).toBe('edited text')
    unsubscribe()
  })
})
