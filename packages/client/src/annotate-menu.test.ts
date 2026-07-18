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

test('opens a 4-row menu (active / enrolled / feedback / close)', () => {
  openAnnotateMenu()
  expect(menu()?.classList.contains('visible')).toBe(true)
  const labels = rows().map(r => r.querySelector('.pa-am-label')?.textContent ?? '')
  expect(labels.length).toBe(4)
  expect(labels[0]).toContain('active channel')
  expect(labels[1]).toContain('enrolled channel')
  expect(labels[2]).toContain('Give feedback')
  expect(labels[3]).toContain('Close')
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

test('choosing "active" sets target and closes (no exit)', () => {
  agentInfo.set('mayor', { id: 'mayor', name: 'Mayor', color: '#fff' })
  ;(window as any).__paPinChannel = 'mayor'
  let exited = 0
  wireAnnotateMenu(() => { exited++ })
  openAnnotateMenu()
  rows()[0].click()
  expect(currentSendTarget()).toBe('active')
  expect(exited).toBe(0)
  expect(menu()?.classList.contains('visible')).toBe(false)
})

test('choosing "enrolled" sets target and closes (no exit)', () => {
  agentInfo.set('mayor', { id: 'mayor', name: 'Mayor', color: '#fff' })
  ;(window as any).__paPinChannel = 'mayor'
  setSendTarget('active')
  let exited = 0
  wireAnnotateMenu(() => { exited++ })
  openAnnotateMenu()
  rows()[1].click()
  expect(currentSendTarget()).toBe('enrolled')
  expect(exited).toBe(0)
  expect(menu()?.classList.contains('visible')).toBe(false)
})

test('a disabled enrolled row cannot be clicked (has no handler)', () => {
  let exited = 0
  wireAnnotateMenu(() => { exited++ })
  openAnnotateMenu()
  const enrolled = rows()[1]
  // Disabled rows have no click handler, so clicking doesn't change routing
  const targetBefore = currentSendTarget()
  enrolled.click()
  const targetAfter = currentSendTarget()
  expect(targetBefore).toEqual(targetAfter)   // routing unchanged
  expect(exited).toBe(0)
})

test('Close dismisses and calls exit callback', () => {
  let exited = 0
  wireAnnotateMenu(() => { exited++ })
  openAnnotateMenu()
  rows()[3].click()  // Close is now row 3 (feedback moved to 2)
  expect(exited).toBe(1)
  expect(menu()?.classList.contains('visible')).toBe(false)
})

test('the current target row shows a checkmark', () => {
  setSendTarget('active')
  openAnnotateMenu()
  const checks = rows().map(r => r.querySelector('.pa-am-check')?.textContent ?? '')
  expect(checks[0]).toBe('✓')   // active is current
  expect(checks[1]).toBe('')
  expect(checks[2]).toBe('')    // feedback has no checkmark
  expect(checks[3]).toBe('')    // close has no checkmark
})

test('Escape closes the open menu', () => {
  openAnnotateMenu()
  expect(menu()?.classList.contains('visible')).toBe(true)
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
  expect(menu()?.classList.contains('visible')).toBe(false)
})
