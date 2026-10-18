import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, describe, beforeEach } from 'bun:test'
import { trapTabKey } from './focus-trap'

// The picker's aria-modal="true" dialogs (channel-picker / sender-picker) rely
// on this helper to keep Tab inside the modal: aria-modal only hides the rest
// of the page from assistive tech, it does nothing about the keyboard.

function modal(): HTMLElement {
  const el = document.createElement('div')
  el.innerHTML = `
    <button id="close">✕</button>
    <input id="field" type="text" />`
  document.body.appendChild(el)
  return el
}

function tab(active: HTMLElement, shiftKey = false): KeyboardEvent {
  const e = new KeyboardEvent('keydown', { key: 'Tab', shiftKey, bubbles: true, cancelable: true })
  active.dispatchEvent(e)
  return e
}

beforeEach(() => { document.body.innerHTML = '' })

describe('trapTabKey', () => {
  test('Tab on the last focusable wraps to the first', () => {
    const el = modal(); trapTabKey(el)
    const field = document.getElementById('field') as HTMLInputElement
    field.focus()
    const e = tab(field)
    expect(e.defaultPrevented).toBe(true)
    expect(document.activeElement?.id).toBe('close')
  })

  test('Shift+Tab on the first focusable wraps to the last', () => {
    const el = modal(); trapTabKey(el)
    const close = document.getElementById('close') as HTMLButtonElement
    close.focus()
    const e = tab(close, true)
    expect(e.defaultPrevented).toBe(true)
    expect(document.activeElement?.id).toBe('field')
  })

  test('Tab in the middle of the list is left to the browser', () => {
    const el = modal(); trapTabKey(el)
    const extra = document.createElement('button')
    extra.id = 'mid'; el.appendChild(extra)
    const close = document.getElementById('close') as HTMLButtonElement
    close.focus()
    expect(tab(close).defaultPrevented).toBe(false)
    expect(document.activeElement?.id).toBe('close')
  })

  test('Tab pressed while focus is outside the modal pulls it back in', () => {
    const outside = document.createElement('textarea')
    outside.id = 'pa-input'; document.body.appendChild(outside)
    const el = modal(); trapTabKey(el)
    outside.focus()
    expect(tab(outside).defaultPrevented).toBe(true)
    expect(document.activeElement?.id).toBe('close')
  })

  test('non-Tab keys are never hijacked', () => {
    const el = modal(); trapTabKey(el)
    const field = document.getElementById('field') as HTMLInputElement
    field.focus()
    const e = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    field.dispatchEvent(e)
    expect(e.defaultPrevented).toBe(false)
    expect(document.activeElement?.id).toBe('field')
  })

  test('the returned teardown removes the handler', () => {
    const el = modal(); const release = trapTabKey(el)
    release()
    const field = document.getElementById('field') as HTMLInputElement
    field.focus()
    expect(tab(field).defaultPrevented).toBe(false)
    expect(document.activeElement?.id).toBe('field')
  })

  test('a detached modal never swallows Tab for the rest of the page', () => {
    const el = modal(); trapTabKey(el)   // teardown deliberately NOT called
    el.remove()
    const outside = document.createElement('textarea')
    outside.id = 'pa-input'; document.body.appendChild(outside)
    outside.focus()
    expect(tab(outside).defaultPrevented).toBe(false)
  })
})