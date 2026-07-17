import { test, expect, describe, beforeEach, afterEach } from 'bun:test'
import {
  resolvePinnedChannel,
  getEnrolledChannel,
  setSendTarget,
  currentSendTarget,
  wireChannelPin,
  renderPinIndicator,
  _resetChannelPinForTests,
} from './channel-pin'
import { agentInfo, setActiveChannel } from './state'

// ── Contract of the mindful channel pin ───────────────────────────────────────
// A page enrolls with window.__paPinChannel='<agentId>'. When enrolled (the
// default target), sends route to the pin regardless of the active tab. 'active'
// is the deliberate, sticky escape. With no valid pin, routing is byte-identical
// to the old activeChannel ?? __paLavishChannel path. These tests pin every
// branch of resolution, the menu API, and the passive indicator.

const w = () => window as any

function seedAgents() {
  agentInfo.clear()
  agentInfo.set('main', { id: 'main', name: 'Main', color: '#111', nicknames: ['First Mate'] })
  agentInfo.set('mayor', { id: 'mayor', name: 'Mayor', color: '#222' })
}

beforeEach(() => {
  document.body.innerHTML = ''
  document.head.innerHTML = ''
  seedAgents()
  setActiveChannel(null)
  delete w().__paPinChannel
  delete w().__paLavishChannel
  _resetChannelPinForTests()
})

afterEach(() => {
  delete w().__paPinChannel
  delete w().__paLavishChannel
  _resetChannelPinForTests()
})

// ── resolvePinnedChannel — pin resolution ─────────────────────────────────────

describe('resolvePinnedChannel', () => {
  test('undefined when no pin is declared (non-breaking: falls back to old routing)', () => {
    expect(resolvePinnedChannel()).toBeUndefined()
  })

  test('returns the pin id when a valid pin is declared and target is enrolled (default)', () => {
    w().__paPinChannel = 'mayor'
    expect(currentSendTarget()).toBe('enrolled') // default
    expect(resolvePinnedChannel()).toBe('mayor')
  })

  test('undefined when the declared pin is not a known channel (never route to an unseen channel)', () => {
    w().__paPinChannel = 'ghost'
    expect(resolvePinnedChannel()).toBeUndefined()
  })

  test('undefined for an empty-string or non-string pin', () => {
    w().__paPinChannel = ''
    expect(resolvePinnedChannel()).toBeUndefined()
    w().__paPinChannel = 42
    expect(resolvePinnedChannel()).toBeUndefined()
    w().__paPinChannel = { id: 'mayor' }
    expect(resolvePinnedChannel()).toBeUndefined()
  })

  test('undefined while escaped to active, even with a valid pin', () => {
    w().__paPinChannel = 'mayor'
    setSendTarget('active')
    expect(resolvePinnedChannel()).toBeUndefined()
  })

  test('re-pinning after an escape restores the pin', () => {
    w().__paPinChannel = 'mayor'
    setSendTarget('active')
    expect(resolvePinnedChannel()).toBeUndefined()
    setSendTarget('enrolled')
    expect(resolvePinnedChannel()).toBe('mayor')
  })

  test('reads the pin LIVE — a pin set after load is honored without reload', () => {
    expect(resolvePinnedChannel()).toBeUndefined()
    w().__paPinChannel = 'main'
    expect(resolvePinnedChannel()).toBe('main')
  })

  test('a pin that becomes unknown (agent removed) stops resolving', () => {
    w().__paPinChannel = 'mayor'
    expect(resolvePinnedChannel()).toBe('mayor')
    agentInfo.delete('mayor')
    expect(resolvePinnedChannel()).toBeUndefined()
  })
})

// ── getEnrolledChannel — the menu's "enrolled" row ────────────────────────────

describe('getEnrolledChannel', () => {
  test('undefined with no valid pin', () => {
    expect(getEnrolledChannel()).toBeUndefined()
    w().__paPinChannel = 'ghost'
    expect(getEnrolledChannel()).toBeUndefined()
  })

  test('resolves id + display name (first nickname) for a valid pin', () => {
    w().__paPinChannel = 'main'
    expect(getEnrolledChannel()).toEqual({ id: 'main', name: 'First Mate' })
  })

  test('falls back to the registered name when there is no nickname', () => {
    w().__paPinChannel = 'mayor'
    expect(getEnrolledChannel()).toEqual({ id: 'mayor', name: 'Mayor' })
  })

  test('independent of the current send target (the enrolled channel exists even while escaped)', () => {
    w().__paPinChannel = 'mayor'
    setSendTarget('active')
    expect(getEnrolledChannel()).toEqual({ id: 'mayor', name: 'Mayor' })
    expect(resolvePinnedChannel()).toBeUndefined() // but sends don't go there
  })
})

// ── setSendTarget / currentSendTarget — the menu's conscious choice ───────────

describe('send-target mode', () => {
  test('defaults to enrolled', () => {
    expect(currentSendTarget()).toBe('enrolled')
  })

  test('setSendTarget records the choice and it sticks', () => {
    setSendTarget('active')
    expect(currentSendTarget()).toBe('active')
    setSendTarget('enrolled')
    expect(currentSendTarget()).toBe('enrolled')
  })

  test('mode is independent of whether a pin exists (menu can pre-set it)', () => {
    setSendTarget('active')
    expect(currentSendTarget()).toBe('active')
    w().__paPinChannel = 'mayor'
    // enrolling later does not silently flip the human's chosen mode
    expect(currentSendTarget()).toBe('active')
    expect(resolvePinnedChannel()).toBeUndefined()
  })
})

// ── Indicator — the always-truthful passive signal ────────────────────────────

describe('wireChannelPin + indicator', () => {
  test('mounts a single indicator into the input footer, idempotently', () => {
    document.body.innerHTML = '<div id="pa-input-area"><div id="pa-hint">⌘↵ send</div></div>'
    wireChannelPin()
    wireChannelPin()
    const els = document.querySelectorAll('#pa-pin-indicator')
    expect(els.length).toBe(1)
    // Placed BEFORE the hint inside the input area.
    const area = document.getElementById('pa-input-area')!
    expect(area.firstElementChild?.id).toBe('pa-pin-indicator')
  })

  test('hidden (pa-pin-off) when no pin is declared — zero visual change for un-pinned pages', () => {
    document.body.innerHTML = '<div id="pa-input-area"><div id="pa-hint"></div></div>'
    wireChannelPin()
    const el = document.getElementById('pa-pin-indicator')!
    expect(el.classList.contains('pa-pin-off')).toBe(true)
    expect(el.textContent).toBe('')
    expect(el.getAttribute('aria-hidden')).toBe('true')
  })

  test('shows the enrolled target when pinned', () => {
    document.body.innerHTML = '<div id="pa-input-area"><div id="pa-hint"></div></div>'
    w().__paPinChannel = 'main'
    wireChannelPin()
    const el = document.getElementById('pa-pin-indicator')!
    expect(el.classList.contains('pa-pin-on')).toBe(true)
    expect(el.classList.contains('pa-pin-escaped')).toBe(false)
    expect(el.textContent).toBe('→ First Mate')
  })

  test('shows the ACTIVE tab target (escaped styling) after escape', () => {
    document.body.innerHTML = '<div id="pa-input-area"><div id="pa-hint"></div></div>'
    w().__paPinChannel = 'main'
    setActiveChannel('mayor')
    wireChannelPin()
    setSendTarget('active') // escape → follow active tab
    const el = document.getElementById('pa-pin-indicator')!
    expect(el.classList.contains('pa-pin-escaped')).toBe(true)
    expect(el.textContent).toBe('→ Mayor') // the active tab, resolved to its name
  })

  test('tapping the indicator toggles the mode and re-renders', () => {
    document.body.innerHTML = '<div id="pa-input-area"><div id="pa-hint"></div></div>'
    w().__paPinChannel = 'main'
    setActiveChannel('mayor')
    wireChannelPin()
    const el = document.getElementById('pa-pin-indicator') as HTMLButtonElement
    expect(currentSendTarget()).toBe('enrolled')
    el.click()
    expect(currentSendTarget()).toBe('active')
    expect(el.textContent).toBe('→ Mayor')
    el.click()
    expect(currentSendTarget()).toBe('enrolled')
    expect(el.textContent).toBe('→ First Mate')
  })

  test('indicator falls back to __paLavishChannel target when escaped with no active tab', () => {
    document.body.innerHTML = '<div id="pa-input-area"><div id="pa-hint"></div></div>'
    w().__paPinChannel = 'main'
    w().__paLavishChannel = 'mayor'
    setActiveChannel(null)
    wireChannelPin()
    setSendTarget('active')
    const el = document.getElementById('pa-pin-indicator')!
    expect(el.textContent).toBe('→ Mayor')
  })

  test('renderPinIndicator is a safe no-op when the indicator is not mounted', () => {
    expect(() => renderPinIndicator()).not.toThrow()
  })

  test('wireChannelPin falls back to body when no input area exists', () => {
    w().__paPinChannel = 'main'
    wireChannelPin()
    const el = document.getElementById('pa-pin-indicator')
    expect(el).not.toBeNull()
    expect(el?.parentElement).toBe(document.body)
  })
})
