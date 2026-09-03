import { test, expect, describe, beforeEach } from 'bun:test'
import { parlayInput, type ActionEnvelope } from '../index'
import { harness, sleep, STREAM } from './harness'

beforeEach(() => {
  try { localStorage.clear() } catch {}
})

// ── Debounce ────────────────────────────────────────────────────────────────

describe('debounce (scheduleEval settle)', () => {
  test('collapses a burst of edits into ONE eval of the stabilized text', async () => {
    const h = harness()
    for (const v of ['a', 'ab', 'abc']) {
      h.el.value = v
      h.el.dispatchEvent(new Event('input'))
    }
    await sleep(25)
    expect(h.evalCalls).toHaveLength(1)
    expect(h.evalCalls[0].text).toBe('abc')
    // version bumped once per edit; the single fired POST carries the latest.
    expect(h.evalCalls[0].version).toBe(3)
    h.unsub()
  })

  test('every edit bumps the version token', async () => {
    const h = harness()
    h.el.value = 'x'
    h.el.dispatchEvent(new Event('input'))
    await sleep(25)
    h.el.value = 'xy'
    h.el.dispatchEvent(new Event('input'))
    await sleep(25)
    expect(h.evalCalls.map(c => c.version)).toEqual([1, 2])
    h.unsub()
  })
})

// ── Version staleness ─────────────────────────────────────────────────────────

describe('version staleness rejection', () => {
  test('drops a mutating action computed against older text and resyncs', async () => {
    const h = harness()
    h.el.value = 'hi'
    h.el.dispatchEvent(new Event('input')) // version → 1
    await sleep(25)
    const before = h.evalCalls.length

    // Action computed against version 0 — the user has since typed 'hi'.
    h.push({ baseVersion: 0, seq: 0, actions: [{ verb: 'setText', args: { text: 'STALE' } }] })

    // The stale setText must NOT touch the buffer...
    expect(h.el.value).toBe('hi')
    expect(h.applies).toContain('rejected-stale')
    // ...and a resync POST must re-anchor the server on the current text.
    expect(h.evalCalls.length).toBe(before + 1)
    expect(h.evalCalls[h.evalCalls.length - 1].text).toBe('hi')
    h.unsub()
  })

  test('applies a current (non-stale) mutating action', () => {
    const h = harness()
    // No edits: version is 0, so baseVersion 0 is not stale.
    h.push({ baseVersion: 0, seq: 0, actions: [{ verb: 'setText', args: { text: 'from server' } }] })
    expect(h.el.value).toBe('from server')
    expect(h.applies).toContain('applied')
    h.unsub()
  })

  test('a stale NON-mutating action is still applied (only mutations gate)', () => {
    const h = harness({ onAction: () => {} })
    h.el.value = 'typed'
    h.el.dispatchEvent(new Event('input')) // version → 1
    // baseVersion 0 < 1 but the action does not mutate the buffer.
    h.push({ baseVersion: 0, seq: 0, actions: [{ verb: 'showHint', args: { text: 'hi' } }] })
    expect(h.applies).toContain('applied')
    expect(h.applies).not.toContain('rejected-stale')
    h.unsub()
  })
})

// ── Seq ordering ──────────────────────────────────────────────────────────────

describe('seq-gap resync', () => {
  test('a gap in seq triggers a resync', () => {
    const h = harness()
    // Establish the expected seq at 1.
    h.push({ baseVersion: 0, seq: 0, actions: [{ verb: 'noop' }] })
    const before = h.evalCalls.length

    // Jump to seq 5 — four events were dropped in transit.
    h.push({ baseVersion: 0, seq: 5, actions: [{ verb: 'noop' }] })
    expect(h.applies).toContain('resync')
    expect(h.evalCalls.length).toBe(before + 1) // resync POST fired
    h.unsub()
  })

  test('in-order seq does NOT resync', () => {
    const h = harness()
    h.push({ baseVersion: 0, seq: 0, actions: [{ verb: 'noop' }] })
    const before = h.evalCalls.length
    h.push({ baseVersion: 0, seq: 1, actions: [{ verb: 'noop' }] })
    expect(h.applies).not.toContain('resync')
    expect(h.evalCalls.length).toBe(before) // no extra POST
    h.unsub()
  })
})

// ── Apply only from SSE ────────────────────────────────────────────────────────

describe('actions are applied ONLY from the SSE stream', () => {
  test('the synchronous eval POST response never mutates the buffer', async () => {
    const el = document.createElement('input')
    const evalCalls: any[] = []
    let sseHandler: ((env: ActionEnvelope) => void) | null = null
    // The POST response CARRIES actions — a naive client would apply them.
    const fetchImpl = ((url: string, init?: RequestInit) => {
      if (String(url).endsWith('/api/chat/eval')) {
        evalCalls.push(init?.body ? JSON.parse(init.body as string) : undefined)
        return Promise.resolve(new Response(JSON.stringify({
          ok: true, v: 1, streamId: STREAM, seq: 0, baseVersion: 1,
          actions: [{ verb: 'setText', args: { text: 'FROM_POST_RESPONSE' } }],
        }), { headers: { 'Content-Type': 'application/json' } }))
      }
      return Promise.resolve(new Response('{}'))
    }) as unknown as typeof fetch

    const unsub = parlayInput(el, {
      server: 'http://localhost:4242', settleMs: 5, fetch: fetchImpl,
      subscribe: (_e, handler) => { sseHandler = handler; return () => {} },
    })

    el.value = 'typed'
    el.dispatchEvent(new Event('input'))
    await sleep(25)
    expect(evalCalls).toHaveLength(1)
    // The POST response's actions were IGNORED.
    expect(el.value).toBe('typed')

    // The SAME action arriving over SSE IS applied.
    sseHandler!({ v: 1, streamId: STREAM, seq: 0, baseVersion: 1, actions: [{ verb: 'setText', args: { text: 'FROM_SSE' } }] })
    expect(el.value).toBe('FROM_SSE')
    unsub()
  })
})

// ── submitNow tail re-verification (irreversibility guard) ─────────────────────

describe('submitNow re-verifies the tail against the live buffer', () => {
  test('fires when the required tail is still at the end', () => {
    const h = harness()
    h.el.value = 'hello world please send'
    h.push({ baseVersion: 0, seq: 0, actions: [{ verb: 'submitNow', args: { requireTail: 'send' } }] })
    expect(h.sendCalls).toHaveLength(1)
    expect(h.sendCalls[0].text).toBe('hello world please')
    h.unsub()
  })

  test('does NOT fire when the tail has moved on (the slow-link race)', () => {
    const h = harness()
    h.el.value = 'hello send but I kept typing'
    h.push({ baseVersion: 0, seq: 0, actions: [{ verb: 'submitNow', args: { requireTail: 'send' } }] })
    expect(h.sendCalls).toHaveLength(0)
    expect(h.applies).toContain('rejected-stale')
    h.unsub()
  })

  test('onSubmit override receives the message instead of POST /send', () => {
    const submitted: string[] = []
    const h = harness({ onSubmit: (t) => { submitted.push(t) } })
    h.el.value = 'ship it now'
    h.push({ baseVersion: 0, seq: 0, actions: [{ verb: 'submitNow', args: { text: 'ship it now' } }] })
    expect(submitted).toEqual(['ship it now'])
    expect(h.sendCalls).toHaveLength(0)
    h.unsub()
  })
})

// ── Unknown verbs delegate ─────────────────────────────────────────────────────

describe('non-core verbs delegate to onAction', () => {
  test('a UI verb is handed to onAction, not applied to the element', () => {
    const seen: string[] = []
    const h = harness({ onAction: (a) => seen.push(a.verb) })
    h.push({ baseVersion: 0, seq: 0, actions: [{ verb: 'openChannelPicker', args: {} }] })
    expect(seen).toEqual(['openChannelPicker'])
    h.unsub()
  })
})

// ── replaceRange (discussion #246: `change sentence` / global edit commands) ──
// A built-in, applied to the element directly — never delegated to onAction —
// so every wrapped input gets the inline edit commands with zero embedder work.

describe('replaceRange', () => {
  test('canonical trace: splices [start,end) with text and collapses the cursor to start', () => {
    const h = harness()
    h.el.value = 'foo foo. bar bar. baz baz'
    const start = 'foo foo. '.length
    const end = 'foo foo. bar bar'.length
    h.push({ baseVersion: 0, seq: 0, actions: [{ verb: 'replaceRange', args: { start, end, text: '' } }] })
    expect(h.el.value).toBe('foo foo. . baz baz')
    expect(h.el.selectionStart).toBe(start)
    expect(h.el.selectionEnd).toBe(start)
    expect(h.applies).toContain('applied')
    h.unsub()
  })

  test('a non-empty replacement collapses the cursor to the end of the inserted text', () => {
    const h = harness()
    h.el.value = 'hello world'
    h.push({ baseVersion: 0, seq: 0, actions: [{ verb: 'replaceRange', args: { start: 0, end: 5, text: 'goodbye' } }] })
    expect(h.el.value).toBe('goodbye world')
    expect(h.el.selectionStart).toBe('goodbye'.length)
    h.unsub()
  })

  test('is treated as mutating — a stale replaceRange is rejected and resyncs', async () => {
    const h = harness()
    h.el.value = 'hi'
    h.el.dispatchEvent(new Event('input')) // version → 1
    await sleep(25)
    h.push({ baseVersion: 0, seq: 0, actions: [{ verb: 'replaceRange', args: { start: 0, end: 2, text: '' } }] })
    expect(h.el.value).toBe('hi')
    expect(h.applies).toContain('rejected-stale')
    h.unsub()
  })
})
