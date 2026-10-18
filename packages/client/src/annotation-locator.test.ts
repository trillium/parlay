import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, describe, beforeEach } from 'bun:test'
import { resolveLocator } from './annotation-locator'

// These cover the text-heuristic fallback only (steps 2 and 3 of the documented
// robustness ordering in annotation-locator.ts); the structural round-trip and
// the Parlay-UI rejection live in annotation-store.test.ts.
//
// The stale-locator scenario in every test below: the page re-rendered, so the
// saved nth-of-type path misses and the resolver falls back to text. Getting
// that fallback wrong re-anchors an annotation onto an unrelated element,
// which is worse than losing the marker — so a wrong hit must never win.

const STALE = 'body > nav > a:nth-of-type(9)' // a path that no longer exists

beforeEach(() => { document.body.innerHTML = '' })

describe('resolveLocator text fallback', () => {
  test('does not anchor onto a one-letter element that merely prefixes the label', () => {
    document.body.innerHTML = `<nav><a>L</a><a>Lo</a><a>Log</a><a>Logo</a><a>Logout</a></nav>`
    const resolved = resolveLocator(STALE, 'Logout')
    expect(resolved?.textContent).toBe('Logout') // the real button, not the logo
  })

  test('prefers a full forward match appearing after a shortened candidate', () => {
    // "Log" is a reverse-prefix match for the label "Logout", but it appears
    // first in document order — the forward pass must still win.
    document.body.innerHTML = `<nav><a>Log</a><a>Logout</a></nav>`
    const resolved = resolveLocator(STALE, 'Logout')
    expect(resolved?.textContent).toBe('Logout')
  })

  test('does not anchor onto a short unrelated element that shares a prefix', () => {
    // Only "Logo" exists here, so the forward pass finds nothing and the
    // reverse pass must reject the 4-char brand link as too small a fraction of
    // the saved label — a markerless note beats a marker on the wrong element.
    document.body.innerHTML = `<nav><a>Logo</a></nav>`
    expect(resolveLocator(STALE, 'Logout account')).toBeNull()
  })

  test('resolves onto a candidate whose live text has shrunk, if the prefix is substantial', () => {
    // Streaming output stopped early: the element's text is now a prefix of
    // the label captured when the annotation was made.
    document.body.innerHTML = `<nav><a>Logout</a></nav>`
    expect(resolveLocator(STALE, 'Logout account')?.textContent).toBe('Logout')
  })

  test('returns null rather than anchor onto a near-empty element', () => {
    document.body.innerHTML = `<nav><a>S</a><a>i</a><a>g</a></nav>`
    expect(resolveLocator(STALE, 'Sign in')).toBeNull()
  })

  test('empty element text is never a match', () => {
    document.body.innerHTML = `<nav><a></a><a>Logout</a></nav>`
    expect(resolveLocator(STALE, 'Logout')?.textContent).toBe('Logout')
  })

  test('never re-anchors onto Parlay UI through the fallback passes', () => {
    document.body.innerHTML = `<div id="pa-drawer"><a>Logout account</a></div><nav><a>Logout</a></nav>`
    expect(resolveLocator(STALE, 'Logout account')?.closest('#pa-drawer')).toBeNull()
  })
})
