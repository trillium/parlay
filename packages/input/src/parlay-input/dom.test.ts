import { test, expect, describe, afterEach } from 'bun:test'
import { writeCursor, getDeviceId } from './dom'

describe('writeCursor', () => {
  test('places the caret on a text input', () => {
    const el = document.createElement('input')
    el.value = 'hello'
    writeCursor(el, 2)
    expect(el.selectionStart).toBe(2)
    expect(el.selectionEnd).toBe(2)
  })

  test('is a no-op on a div', () => {
    expect(() => writeCursor(document.createElement('div'), 1)).not.toThrow()
  })

  test('does not call setSelectionRange on input[type=number] (real browsers throw InvalidStateError)', () => {
    const el = document.createElement('input')
    el.type = 'number'
    let called = false
    el.setSelectionRange = () => { called = true; throw new DOMException('x', 'InvalidStateError') }
    expect(() => writeCursor(el, 1)).not.toThrow()
    expect(called).toBe(false)
  })

  test('swallows a refusal from an otherwise selectable element', () => {
    const el = document.createElement('input')
    el.setSelectionRange = () => { throw new DOMException('x', 'InvalidStateError') }
    expect(() => writeCursor(el, 1)).not.toThrow()
  })
})

describe('getDeviceId', () => {
  const real = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')!
  afterEach(() => { Object.defineProperty(globalThis, 'localStorage', real) })

  test('returns the pre-write id when persistence fails, not a second one', () => {
    let attempted: string | null = null
    const full = {
      getItem: () => null,
      setItem: (_k: string, v: string) => { attempted = v; throw new DOMException('quota', 'QuotaExceededError') },
    }
    Object.defineProperty(globalThis, 'localStorage', { value: full, configurable: true })
    const id = getDeviceId()
    expect(attempted).not.toBeNull()
    expect(id).toBe(attempted as unknown as string)
  })
})
