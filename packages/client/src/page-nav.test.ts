import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, describe, beforeEach, afterEach, mock } from 'bun:test'
import { injectPageNav, openPageNav, closePageNav } from './page-nav'
import { getSettings, saveSettings } from './settings-modal'

// ── Hands-free mode (noKeyboardMode) ─────────────────────────────────────────
// openPageNav focuses its own search input (not the composer) — confirms the
// same suppression pattern applies to the page-nav picker.
describe('openPageNav — hands-free mode', () => {
  let origFetch: typeof fetch
  beforeEach(() => {
    document.body.innerHTML = ''
    origFetch = globalThis.fetch
    globalThis.fetch = mock(async () => new Response(JSON.stringify({ pages: [] }), { status: 200 })) as any
    injectPageNav()
  })
  afterEach(async () => {
    closePageNav()
    globalThis.fetch = origFetch
    await saveSettings({ ...getSettings(), noKeyboardMode: false })
  })

  test('does not focus the search input when noKeyboardMode is on', async () => {
    await saveSettings({ ...getSettings(), noKeyboardMode: true })
    await openPageNav()
    await new Promise(r => setTimeout(r, 40))
    expect(document.activeElement?.id).not.toBe('pa-nav-search')
  })

  test('still focuses the search input when noKeyboardMode is off (unchanged default)', async () => {
    await saveSettings({ ...getSettings(), noKeyboardMode: false })
    await openPageNav()
    await new Promise(r => setTimeout(r, 40))
    expect(document.activeElement?.id).toBe('pa-nav-search')
  })
})
