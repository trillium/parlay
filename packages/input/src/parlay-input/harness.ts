// Shared test harness for the parlay-input suites.
//
// parlay's protocol is a REST up-channel (POST /api/chat/eval, /api/chat/send)
// + a shared SSE down-channel (input_action envelopes). These fakes drive
// THOSE endpoints — a captured `subscribe` handler stands in for the SSE
// stream, and an injected `fetch` records the up-channel POSTs. No WebSocket
// anywhere: the old transport was invented and never existed.
import { parlayInput, type ActionEnvelope, type ApplyResult, type ParlayInputOptions } from '../index'

export const sleep = (ms: number) => new Promise(r => setTimeout(r, ms))

export const STREAM = 'S1'

export interface Harness {
  el: HTMLInputElement
  evalCalls: any[]
  sendCalls: any[]
  applies: ApplyResult[]
  push: (env: Partial<ActionEnvelope>) => void
  unsub: () => void
}

export function harness(overrides: Partial<ParlayInputOptions> = {}): Harness {
  const el = document.createElement('input')
  const evalCalls: any[] = []
  const sendCalls: any[] = []
  const applies: ApplyResult[] = []
  let sseHandler: ((env: ActionEnvelope) => void) | null = null

  const fetchImpl = ((url: string, init?: RequestInit) => {
    const u = String(url)
    const body = init?.body ? JSON.parse(init.body as string) : undefined
    if (u.endsWith('/api/chat/eval')) evalCalls.push(body)
    else if (u.endsWith('/api/chat/send')) sendCalls.push(body)
    return Promise.resolve(new Response('{}', { headers: { 'Content-Type': 'application/json' } }))
  }) as unknown as typeof fetch

  const unsub = parlayInput(el, {
    server: 'http://localhost:4242',
    settleMs: 5,
    fetch: fetchImpl,
    subscribe: (_event, handler) => { sseHandler = handler; return () => { sseHandler = null } },
    onApply: (r) => applies.push(r),
    ...overrides,
  })

  const push = (partial: Partial<ActionEnvelope>) => {
    const env: ActionEnvelope = { v: 1, streamId: STREAM, seq: 0, baseVersion: 1_000_000, actions: [], ...partial }
    sseHandler!(env)
  }

  return { el, evalCalls, sendCalls, applies, push, unsub }
}

// A minimal EventSource stand-in for the owned-SSE reconnect tests.
export class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  listeners: Record<string, Array<(e: any) => void>> = {}
  onerror: (() => void) | null = null
  constructor(url: string) { this.url = url; FakeEventSource.instances.push(this) }
  addEventListener(type: string, fn: (e: any) => void) { (this.listeners[type] ||= []).push(fn) }
  close() {}
  emit(type: string, data: unknown) { for (const fn of this.listeners[type] ?? []) fn({ data: JSON.stringify(data) }) }
  fireOpen() { for (const fn of this.listeners['open'] ?? []) fn(undefined) }
  fireError() { this.onerror?.() }
}
