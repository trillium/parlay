import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, describe, beforeEach, afterEach, mock } from 'bun:test'
import { openSenderPicker, closeSenderPicker } from './sender-picker'
import type { PickerSender } from './commands/dispatcher/types'
import { getSettings, saveSettings } from './settings-modal'

const SENDERS: PickerSender[] = [
  { index: 1, label: 'Alice', nickname: 'Alice' },
  { index: 2, label: 'Bob',   nickname: 'the boss' },
]

// ── Hands-free mode (noKeyboardMode) ─────────────────────────────────────────
// Mirrors channel-picker.test.ts's hands-free coverage: closeSenderPicker
// returns focus to the composer identically to closeChannelPicker.
describe('closeSenderPicker — hands-free mode', () => {
  let origFetch: typeof fetch
  beforeEach(() => {
    document.body.innerHTML = ''
    origFetch = globalThis.fetch
    globalThis.fetch = mock(async () => new Response('{}', { status: 200 })) as any
    const main = document.createElement('textarea')
    main.id = 'pa-input'
    document.body.appendChild(main)
  })
  afterEach(async () => {
    closeSenderPicker()
    globalThis.fetch = origFetch
    await saveSettings({ ...getSettings(), noKeyboardMode: false })
  })

  test('does not refocus the composer when noKeyboardMode is on', async () => {
    await saveSettings({ ...getSettings(), noKeyboardMode: true })
    openSenderPicker('pick', SENDERS)
    closeSenderPicker()
    await new Promise(r => setTimeout(r, 30))
    expect(document.activeElement?.id).not.toBe('pa-input')
  })

  test('still refocuses the composer when noKeyboardMode is off (unchanged default)', async () => {
    await saveSettings({ ...getSettings(), noKeyboardMode: false })
    openSenderPicker('pick', SENDERS)
    closeSenderPicker()
    await new Promise(r => setTimeout(r, 30))
    expect(document.activeElement?.id).toBe('pa-input')
  })
})

// ── Keyboard containment (aria-modal is one-sided) ──────────────────────────
describe('openSenderPicker — focus stays inside the modal', () => {
  beforeEach(() => { document.body.innerHTML = '' })

  test('Tab on the last focusable wraps to the first instead of leaving the modal', () => {
    openSenderPicker('pick', SENDERS)
    const input = document.getElementById('pa-sender-picker-input') as HTMLInputElement
    input.focus()
    const e = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
    input.dispatchEvent(e)
    expect(e.defaultPrevented).toBe(true)
    expect(document.activeElement?.id).toBe('pa-sender-picker-close')
  })

  test('the input carries an accessible name', () => {
    openSenderPicker('pick', SENDERS)
    expect(document.getElementById('pa-sender-picker-input')?.getAttribute('aria-label'))
      .toBe('Sender name or number')
  })
})
