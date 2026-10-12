/**
 * DOM + identity helpers: read/write a plain `Element`'s buffer, generate ids,
 * and the mutating-verb classification used for staleness rejection.
 */
import type { ActionEnvelope } from './types'

export const LIB_VERSION = '0.2.0'

export function readValue(el: Element): string {
  if ('value' in el) return String((el as HTMLInputElement).value ?? '')
  return el.textContent ?? ''
}

export function writeValue(el: Element, text: string): void {
  if ('value' in el) (el as HTMLInputElement).value = text
  else el.textContent = text
}

export function readCursor(el: Element): { anchor: number; active: number } {
  const input = el as HTMLInputElement
  if (typeof input.selectionStart === 'number' && typeof input.selectionEnd === 'number') {
    return { anchor: input.selectionStart, active: input.selectionEnd }
  }
  return { anchor: 0, active: 0 }
}

// <input> types for which the HTML spec defines a selection. On every other
// type (number, email, date, ...) setSelectionRange EXISTS but throws
// InvalidStateError, so the method's presence is not evidence of support.
const SELECTABLE_INPUT_TYPES = new Set(['text', 'search', 'url', 'tel', 'password'])

/** Collapse the caret to `pos` on elements that support text selection. A
 * plain `div`/`span` textContent target, or an `<input>` whose type has no
 * selection (e.g. number), has none — a harmless no-op there. */
export function writeCursor(el: Element, pos: number): void {
  const input = el as HTMLInputElement
  if (typeof input.setSelectionRange !== 'function') return
  if (el.tagName === 'INPUT' && !SELECTABLE_INPUT_TYPES.has((input.type || 'text').toLowerCase())) return
  try { input.setSelectionRange(pos, pos) } catch { /* selection refused — no-op */ }
}

export function randomId(prefix: string): string {
  const c = (globalThis as { crypto?: Crypto }).crypto
  if (c?.randomUUID) return c.randomUUID()
  return `${prefix}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`
}

/**
 * Stable per-browser device id, persisted to localStorage under `pa-device-id`
 * (the same key the reference client uses, so a page already running the panel
 * shares one identity). Falls back to an ephemeral id when storage is absent.
 */
export function getDeviceId(): string {
  let id: string | null = null
  try {
    const ls = (globalThis as { localStorage?: Storage }).localStorage
    if (ls) {
      id = ls.getItem('pa-device-id')
      if (!id) { id = randomId('dev'); ls.setItem('pa-device-id', id) }
      return id
    }
  } catch { /* storage disabled or full — keep any id already minted */ }
  // If persistence failed after minting, return that same id rather than a second one.
  return id || randomId('dev')
}

// The verbs that MUTATE the buffer. Only these trigger staleness rejection: a
// stale non-mutating action (a hint, a picker) is harmless to apply late.
const MUTATING_VERBS = new Set(['setText', 'clear', 'submitNow', 'replaceRange', 'stripTrigger'])

export function isMutating(env: ActionEnvelope): boolean {
  return env.actions.some(a => MUTATING_VERBS.has(a.verb))
}
