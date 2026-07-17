import { test, expect, beforeEach } from 'bun:test'
import { openAnnotateMenu, closeAnnotateMenu, wireAnnotateMenu } from './annotate-menu'
import { currentSendTarget, setSendTarget, _resetChannelPinForTests } from './channel-pin'
import { agentInfo } from './state'

const MENU = '#pa-annotate-menu'
function menu() { return document.querySelector(MENU) as HTMLElement | null }
function rows() { return Array.from(document.querySelectorAll(`${MENU} .pa-am-row`)) as HTMLElement[] }

beforeEach(() => {
  document.body.innerHTML = ''
  document.head.innerHTML = ''
  const stale = menu(); if (stale) stale.remove()
  closeAnnotateMenu()
  agentInfo.clear()
  ;(window as any).__paPinChannel = undefined
  _resetChannelPinForTests()   // back to default 'enrolled'
})

test('opens a 3-row menu (active / enrolled / close)', () => {
  openAnnotateMenu()
  expect(menu()?.classList.contains('visible')).toBe(true)
  const labels = rows().map(r => r.querySelector('.pa-am-label')?.textContent ?? '')
  expect(labels.length).toBe(3)
  expect(labels[0]).toContain('active channel')
  expect(labels[1]).toContain('enrolled channel')
  expect(labels[2]).toContain('Close')
})

test('enrolled row is disabled when the page declares no pin', () => {
  openAnnotateMenu()
  const enrolled = rows()[1]
  expect(enrolled.getAttribute('aria-disabled')).toBe('true')
  expect(enrolled.textContent).toContain('no enrolled channel')
})

test('enrolled row is enabled and shows the channel name when a valid pin exists', () => {
  agentInfo.set('mayor', { id: 'mayor', name: 'Mayor', color: '#fff' })
  ;(window as any).__paPinChannel = 'mayor'
  openAnnotateMenu()
  const enrolled = rows()[1]
  expect(enrolled.getAttribute('aria-disabled')).toBeNull()
  expect(enrolled.textContent).toContain('Mayor')
})

test('choosing "active" sets target, closes, and arms', () => {
  agentInfo.set('mayor', { id: 'mayor', name: 'Mayor', color: '#fff' })
  ;(window as any).__paPinChannel = 'mayor'
  let armed = 0
  wireAnnotateMenu(() => { armed++ })
  openAnnotateMenu()
  rows()[0].click()
  expect(currentSendTarget()).toBe('active')
  expect(armed).toBe(1)
  expect(menu()?.classList.contains('visible')).toBe(false)
})

test('choosing "enrolled" sets target, closes, and arms', () => {
  agentInfo.set('mayor', { id: 'mayor', name: 'Mayor', color: '#fff' })
  ;(window as any).__paPinChannel = 'mayor'
  setSendTarget('active')
  let armed = 0
  wireAnnotateMenu(() => { armed++ })
  openAnnotateMenu()
  rows()[1].click()
  expect(currentSendTarget()).toBe('enrolled')
  expect(armed).toBe(1)
  expect(menu()?.classList.contains('visible')).toBe(false)
})

test('a disabled enrolled row does nothing when clicked', () => {
  let armed = 0
  wireAnnotateMenu(() => { armed++ })
  openAnnotateMenu()
  rows()[1].click()   // disabled (no pin)
  expect(armed).toBe(0)
  expect(menu()?.classList.contains('visible')).toBe(true)
})

test('Close dismisses without arming', () => {
  let armed = 0
  wireAnnotateMenu(() => { armed++ })
  openAnnotateMenu()
  rows()[2].click()
  expect(armed).toBe(0)
  expect(menu()?.classList.contains('visible')).toBe(false)
})

test('the current target row shows a checkmark', () => {
  setSendTarget('active')
  openAnnotateMenu()
  const checks = rows().map(r => r.querySelector('.pa-am-check')?.textContent ?? '')
  expect(checks[0]).toBe('✓')   // active is current
  expect(checks[1]).toBe('')
})

test('Escape closes the open menu', () => {
  openAnnotateMenu()
  expect(menu()?.classList.contains('visible')).toBe(true)
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
  expect(menu()?.classList.contains('visible')).toBe(false)
})
