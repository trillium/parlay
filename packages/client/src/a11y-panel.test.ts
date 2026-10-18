import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, describe, beforeEach, afterEach } from 'bun:test'
import { initA11yPanel, openA11yPanel } from './a11y-panel'

// ── The a11y panel reads its scales from localStorage, which is untrusted ────
// These pin the two ways that used to break: a corrupt/hostile stored value
// reaching the CSS custom properties (or throwing during init), and the
// per-open document listener that openPanel registered and never removed.

const KEY = 'pa-a11y-settings'

function store(v: unknown) {
  localStorage.setItem(KEY, typeof v === 'string' ? v : JSON.stringify(v))
}

function cssVar(name: string): string {
  return document.documentElement.style.getPropertyValue(name)
}

/** Count document 'click' listeners added minus removed across `fn`. */
function listenerDelta(fn: () => void): number {
  let delta = 0
  const add = document.addEventListener.bind(document)
  const rem = document.removeEventListener.bind(document)
  document.addEventListener = ((t: any, h: any, o: any) => {
    if (t === 'click') delta++
    return add(t, h, o)
  }) as any
  document.removeEventListener = ((t: any, h: any, o: any) => {
    if (t === 'click') delta--
    return rem(t, h, o)
  }) as any
  try { fn() } finally {
    document.addEventListener = add
    document.removeEventListener = rem
  }
  return delta
}

// The panel element is module state, so clearing body.innerHTML alone leaves
// openA11yPanel() bailing out as "already open" — close it the real way.
afterEach(() => {
  document.getElementById('pa-a11y-panel')?.dispatchEvent(
    new MouseEvent('click', { bubbles: true }))
  document.body.innerHTML = ''
  document.head.innerHTML = ''
  document.documentElement.removeAttribute('style')
  localStorage.removeItem(KEY)
})

describe('initA11yPanel — stored settings are sanitized', () => {
  test('no stored value → both scales at the 100% default', () => {
    initA11yPanel()
    expect(cssVar('--pa-drawer-scale')).toBe('1.00')
    expect(cssVar('--pa-page-scale')).toBe('1.00')
  })

  test('a stored JSON `null` does not throw (it used to brick init)', () => {
    store('null')
    expect(() => initA11yPanel()).not.toThrow()
    expect(cssVar('--pa-drawer-scale')).toBe('1.00')
  })

  test('unparseable storage falls back to the defaults', () => {
    store('{not json')
    expect(() => initA11yPanel()).not.toThrow()
    expect(cssVar('--pa-page-scale')).toBe('1.00')
  })

  test('non-numeric scales never reach the CSS as NaN', () => {
    store({ drawerScale: 'zzz', pageScale: -4 })
    initA11yPanel()
    expect(cssVar('--pa-drawer-scale')).not.toContain('NaN')
    expect(cssVar('--pa-page-scale')).not.toContain('NaN')
    // 'zzz' is unusable → 100%; -4 is a number but below the 100% floor → snapped up to 100%.
    expect(cssVar('--pa-drawer-scale')).toBe('1.00')
    expect(cssVar('--pa-page-scale')).toBe('1.00')
  })

  test('a bare non-object (number, string) falls back rather than propagating', () => {
    store(42)
    expect(() => initA11yPanel()).not.toThrow()
    expect(cssVar('--pa-drawer-scale')).toBe('1.00')
  })

  test('a valid stored scale is honored', () => {
    store({ drawerScale: 150, pageScale: 200 })
    initA11yPanel()
    expect(cssVar('--pa-drawer-scale')).toBe('1.50')
    expect(cssVar('--pa-page-scale')).toBe('2.00')
  })

  test('a hand-edited off-step scale snaps to the nearest slider step', () => {
    store({ drawerScale: 160, pageScale: 130 })
    initA11yPanel()
    expect(cssVar('--pa-drawer-scale')).toBe('1.50')   // 160 → 150
    expect(cssVar('--pa-page-scale')).toBe('1.25')     // 130 → 125
  })

  test('one bad field does not discard the other good field', () => {
    store({ drawerScale: 'nope', pageScale: 175 })
    initA11yPanel()
    expect(cssVar('--pa-drawer-scale')).toBe('1.00')
    expect(cssVar('--pa-page-scale')).toBe('1.75')
  })
})

describe('openA11yPanel — listener lifecycle', () => {
  test('open → close leaves zero net document click listeners', () => {
    const delta = listenerDelta(() => {
      openA11yPanel()
      document.getElementById('pa-a11y-panel')?.dispatchEvent(
        new MouseEvent('click', { bubbles: true }))
    })
    // Open registers exactly one; the backdrop click closes and unregisters it.
    expect(delta).toBe(0)
    expect(document.getElementById('pa-a11y-panel')).toBeNull()
  })

  test('repeated open/close cycles do not accumulate listeners', () => {
    const delta = listenerDelta(() => {
      for (let i = 0; i < 5; i++) {
        openA11yPanel()
        document.getElementById('pa-a11y-panel')?.dispatchEvent(
          new MouseEvent('click', { bubbles: true }))
      }
    })
    expect(delta).toBe(0)
  })

  test('backdrop click still closes the panel', () => {
    openA11yPanel()
    const panel = document.getElementById('pa-a11y-panel') as HTMLElement
    expect(panel).not.toBeNull()
    panel.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(document.getElementById('pa-a11y-panel')).toBeNull()
  })

  test('opening twice does not stack two panels', () => {
    openA11yPanel()
    openA11yPanel()
    expect(document.querySelectorAll('#pa-a11y-panel').length).toBe(1)
  })

  test('moving a slider applies and persists the new scale', () => {
    openA11yPanel()
    const slider = document.querySelector('#pa-a11y-panel input[type=range]') as HTMLInputElement
    expect(slider).not.toBeNull()
    slider.value = '150'
    slider.dispatchEvent(new Event('input', { bubbles: true }))
    expect(cssVar('--pa-drawer-scale')).toBe('1.50')
    expect(JSON.parse(localStorage.getItem(KEY) as string).drawerScale).toBe(150)
  })
})