import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, describe, beforeEach, afterEach, mock } from 'bun:test'
import {
  openChannelPicker, closeChannelPicker, pickerHint, pickerIsOpen,
} from './channel-picker'
import type { PickerChannel } from './commands/dispatcher/types'
import { getSettings, saveSettings } from './settings-modal'

// ── Frontend contract of docs/CHANNEL_PICKER_CONTRACT.md ──────────────────────
// The picker RENDERS the backend's perception (openChannelPicker) and FIRES
// pickerInput events (mode:"channel-select") — it never resolves locally. These
// tests pin the render shape and the wire event; resolution is the Go engine's.

const CHANNELS: PickerChannel[] = [
  { index: 1, id: 'main',       label: 'main',  nickname: 'main' },
  { index: 2, id: 'mayor',      label: 'boss',  nickname: 'boss' },
  { index: 3, id: 'parlay-dev', label: 'dev',   nickname: 'dev' },
]

function $(sel: string) { return document.querySelector(sel) }

beforeEach(() => { document.body.innerHTML = ''; document.head.innerHTML = '' })
afterEach(() => { closeChannelPicker() })

describe('openChannelPicker — render', () => {
  test('mounts a single full-screen overlay with prompt + focused input', () => {
    openChannelPicker('Say a channel name, nickname, or number', CHANNELS)
    expect(pickerIsOpen()).toBe(true)
    expect(document.querySelectorAll('#pa-picker-overlay').length).toBe(1)
    expect($('#pa-picker-prompt')?.textContent).toBe('Say a channel name, nickname, or number')
    expect($('#pa-picker-input')).not.toBeNull()
  })

  test('renders one numbered row per channel, in order, with label + nickname', () => {
    openChannelPicker('pick', CHANNELS)
    const rows = [...document.querySelectorAll('.pa-picker-row')]
    expect(rows.length).toBe(3)
    expect(rows[0].querySelector('.pa-picker-num')?.textContent).toBe('1.')
    expect(rows[1].querySelector('.pa-picker-num')?.textContent).toBe('2.')
    expect(rows[1].querySelector('.pa-picker-label')?.textContent).toBe('boss')
    expect(rows[2].querySelector('.pa-picker-num')?.textContent).toBe('3.')
  })

  test('hides the nickname span when nickname equals the label', () => {
    openChannelPicker('pick', [{ index: 1, id: 'main', label: 'main', nickname: 'main' }])
    // label === nickname → no separate "(nick)" span
    expect($('.pa-picker-nick')).toBeNull()
    openChannelPicker('pick', [{ index: 1, id: 'mayor', label: 'Mayor', nickname: 'boss' }])
    expect($('.pa-picker-nick')?.textContent).toBe('(boss)')
  })

  test('escapes untrusted label/nickname/prompt (no HTML injection)', () => {
    openChannelPicker('<img src=x onerror=1>', [
      { index: 1, id: 'x', label: '<b>evil</b>', nickname: '' },
    ])
    // The markup must be inert text, not live elements.
    expect(document.querySelector('#pa-picker-prompt img')).toBeNull()
    expect(document.querySelector('.pa-picker-label b')).toBeNull()
    expect($('.pa-picker-label')?.textContent).toBe('<b>evil</b>')
  })

  test('is idempotent — opening again replaces, never stacks', () => {
    openChannelPicker('a', CHANNELS)
    openChannelPicker('b', CHANNELS)
    expect(document.querySelectorAll('#pa-picker-overlay').length).toBe(1)
    expect($('#pa-picker-prompt')?.textContent).toBe('b')
  })
})

describe('pickerInput event (mode:channel-select)', () => {
  let calls: { url: string; body: any }[]
  let origFetch: typeof fetch

  beforeEach(() => {
    calls = []
    origFetch = globalThis.fetch
    globalThis.fetch = mock(async (url: any, init: any) => {
      calls.push({ url: String(url), body: JSON.parse(init.body) })
      return new Response('{}', { status: 200 })
    }) as any
  })
  afterEach(() => { globalThis.fetch = origFetch })

  test('Enter fires a POST /eval with mode=channel-select and a picker- streamId', async () => {
    openChannelPicker('pick', CHANNELS)
    const input = $('#pa-picker-input') as HTMLInputElement
    input.value = 'mayor'
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    await Promise.resolve(); await Promise.resolve()
    expect(calls.length).toBe(1)
    expect(calls[0].url).toContain('/api/chat/eval')
    expect(calls[0].body.mode).toBe('channel-select')
    expect(calls[0].body.text).toBe('mayor')
    expect(String(calls[0].body.streamId)).toStartWith('picker-')
    expect(Array.isArray(calls[0].body.tabs)).toBe(true)
  })

  test('empty / whitespace input does not fire a request', async () => {
    openChannelPicker('pick', CHANNELS)
    const input = $('#pa-picker-input') as HTMLInputElement
    input.value = '   '
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    await Promise.resolve()
    expect(calls.length).toBe(0)
  })
})

describe('pickerHint + close', () => {
  test('pickerHint shows text without closing the modal', () => {
    openChannelPicker('pick', CHANNELS)
    pickerHint('No channel matched "zzz" — try again')
    const hint = $('#pa-picker-hint')
    expect(hint?.textContent).toBe('No channel matched "zzz" — try again')
    expect(hint?.classList.contains('visible')).toBe(true)
    expect(pickerIsOpen()).toBe(true)
  })

  test('pickerHint is a safe no-op when the modal is already closed', () => {
    expect(() => pickerHint('late')).not.toThrow()
  })

  test('closeChannelPicker removes the overlay', () => {
    openChannelPicker('pick', CHANNELS)
    closeChannelPicker()
    expect(pickerIsOpen()).toBe(false)
    expect($('#pa-picker-overlay')).toBeNull()
  })

  test('Escape key closes the modal', () => {
    openChannelPicker('pick', CHANNELS)
    const overlay = $('#pa-picker-overlay') as HTMLElement
    overlay.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(pickerIsOpen()).toBe(false)
  })
})

// ── Keyboard containment (aria-modal is one-sided) ──────────────────────────
describe('openChannelPicker — focus stays inside the modal', () => {
  const focusableIds = () => {
    const panel = $('#pa-picker-panel') as HTMLElement
    return [...panel.querySelectorAll<HTMLElement>('button,input')].map(el => el.id)
  }

  test('every focusable in the modal is reachable and the input has an accessible name', () => {
    openChannelPicker('pick', CHANNELS)
    expect(focusableIds()).toEqual(['pa-picker-close', 'pa-picker-input'])
    // A placeholder is not an accessible name (axe: form elements must have one).
    expect($('#pa-picker-input')?.getAttribute('aria-label')).toBe('Channel name, nickname, or number')
  })

  test('Tab on the last focusable wraps to the first instead of leaving the modal', () => {
    openChannelPicker('pick', CHANNELS)
    const input = $('#pa-picker-input') as HTMLInputElement
    input.focus()
    const e = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
    input.dispatchEvent(e)
    expect(e.defaultPrevented).toBe(true)
    expect(document.activeElement?.id).toBe('pa-picker-close')
  })

  test('Shift+Tab on the first focusable wraps to the last', () => {
    openChannelPicker('pick', CHANNELS)
    const close = $('#pa-picker-close') as HTMLButtonElement
    close.focus()
    const e = new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true })
    close.dispatchEvent(e)
    expect(e.defaultPrevented).toBe(true)
    expect(document.activeElement?.id).toBe('pa-picker-input')
  })

  test('reopening installs exactly one trap (no stacked handlers)', () => {
    openChannelPicker('a', CHANNELS)
    openChannelPicker('b', CHANNELS)
    const input = $('#pa-picker-input') as HTMLInputElement
    input.focus()
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))
    expect(document.activeElement?.id).toBe('pa-picker-close')
  })
})

// ── Hands-free mode (noKeyboardMode) ─────────────────────────────────────────
describe('closeChannelPicker — hands-free mode', () => {
  let origFetch: typeof fetch
  beforeEach(() => {
    origFetch = globalThis.fetch
    globalThis.fetch = mock(async () => new Response('{}', { status: 200 })) as any
    const main = document.createElement('textarea')
    main.id = 'pa-input'
    document.body.appendChild(main)
  })
  afterEach(async () => {
    globalThis.fetch = origFetch
    await saveSettings({ ...getSettings(), noKeyboardMode: false })
  })

  test('does not refocus the composer when noKeyboardMode is on', async () => {
    await saveSettings({ ...getSettings(), noKeyboardMode: true })
    openChannelPicker('pick', CHANNELS)
    closeChannelPicker()
    await new Promise(r => setTimeout(r, 30))
    expect(document.activeElement?.id).not.toBe('pa-input')
  })

  test('still refocuses the composer when noKeyboardMode is off (unchanged default)', async () => {
    await saveSettings({ ...getSettings(), noKeyboardMode: false })
    openChannelPicker('pick', CHANNELS)
    closeChannelPicker()
    await new Promise(r => setTimeout(r, 30))
    expect(document.activeElement?.id).toBe('pa-input')
  })
})
