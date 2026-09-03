import { test, expect, describe } from 'bun:test'
import { setDispatcherContext, applyEnvelope } from './apply'
import type { CommandContext } from '../types'
import type { ActionEnvelope } from './types'

// ── replaceRange dispatch (discussion #246: `change sentence`) ─────────────────
// The engine resolves start/end/text; apply.ts's job is purely mechanical:
// splice, then collapse the cursor to the end of the replacement.

function fakeCtx(initial: string): { ctx: CommandContext; value: () => string; selection: () => { anchor: number; active: number } } {
  let value = initial
  let sel = { anchor: value.length, active: value.length }
  const ctx: CommandContext = {
    input: {
      value: () => value,
      setText: (t: string) => { value = t },
      clear: () => { value = '' },
      submit: () => {},
      selection: () => sel,
      setSelection: (anchor: number, active: number) => { sel = { anchor, active } },
    },
    tabs: { list: () => [], active: () => null, switch: () => true, archive: () => true, next: () => {}, prev: () => {} },
    drawer: { open: () => {} },
    speech: { stop: () => {} },
    settings: { get: () => ({}) as any },
    workspace: { navigate: () => true, present: () => true },
  }
  return { ctx, value: () => value, selection: () => sel }
}

function envelope(streamId: string, seq: number, baseVersion: number, actions: ActionEnvelope['actions']): ActionEnvelope {
  return { v: 1, streamId, seq, baseVersion, actions }
}

describe('applyAction — replaceRange', () => {
  test('canonical trace: removes the sentence + trigger phrase, leaves the cursor at start', () => {
    // "foo foo. bar bar" with the trigger already stripped conceptually — the
    // engine already resolved start/end; apply.ts just executes the splice.
    const text = 'foo foo. bar bar. baz baz'
    const { ctx, value, selection } = fakeCtx(text)
    setDispatcherContext(ctx)
    const start = 'foo foo. '.length // 9
    const end = 'foo foo. bar bar'.length // 16 — the cursor position
    const r = applyEnvelope(envelope('s', 1, 1, [{ verb: 'replaceRange', args: { start, end, text: '' } }]), () => {})
    expect(r).toBe('applied')
    expect(value()).toBe('foo foo. . baz baz')
    expect(selection()).toEqual({ anchor: start, active: start })
  })

  test('empty replacement text collapses the cursor to the deletion start', () => {
    const { ctx, value, selection } = fakeCtx('hello world')
    setDispatcherContext(ctx)
    applyEnvelope(envelope('s', 1, 1, [{ verb: 'replaceRange', args: { start: 5, end: 11, text: '' } }]), () => {})
    expect(value()).toBe('hello')
    expect(selection()).toEqual({ anchor: 5, active: 5 })
  })

  test('a non-empty replacement text collapses the cursor to the end of the inserted text', () => {
    const { ctx, value, selection } = fakeCtx('hello world')
    setDispatcherContext(ctx)
    applyEnvelope(envelope('s', 1, 1, [{ verb: 'replaceRange', args: { start: 0, end: 5, text: 'goodbye' } }]), () => {})
    expect(value()).toBe('goodbye world')
    expect(selection()).toEqual({ anchor: 7, active: 7 })
  })
})
