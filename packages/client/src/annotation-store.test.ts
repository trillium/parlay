import { test, expect, describe, beforeEach, afterEach } from 'bun:test'
import { annotations, markerMap, type Annotation } from './state'
import {
  initAnnotationPersistence,
  persistAnnotations,
  clearPersistedAnnotations,
  wireAnnotationStore,
} from './annotation-store'
import { buildLocator, resolveLocator } from './annotation-locator'

// ── Test doubles for the two closures annotation.ts injects ───────────────────
// The strip re-render is a spy; the marker-add appends a real marker node and
// records it in markerMap exactly like the production addMarker closure, so
// rehydration marker behavior can be asserted on the live DOM.
let rerenderCalls = 0
function fakeRerender() { rerenderCalls++ }
function fakeAddMarker(el: HTMLElement, num: number) {
  if (markerMap.has(el)) return
  const m = document.createElement('div')
  m.className = 'pa-ann-marker'
  m.textContent = String(num)
  el.appendChild(m)
  markerMap.set(el, m)
}

function resetState() {
  annotations.length = 0
  rerenderCalls = 0
  try { localStorage.clear() } catch { /* ignore */ }
}

beforeEach(() => {
  document.body.innerHTML = ''
  resetState()
  wireAnnotationStore(fakeRerender, fakeAddMarker)
})
afterEach(() => { resetState() })

// ── Locator round-trip ────────────────────────────────────────────────────────
describe('buildLocator + resolveLocator', () => {
  test('builds a structural path that re-resolves to the same element', () => {
    document.body.innerHTML = `
      <section><p>alpha</p><p id="target">bravo</p><p>charlie</p></section>`
    const el = document.getElementById('target') as HTMLElement
    const loc = buildLocator(el)
    expect(loc).toContain('#target')            // anchors on the usable id
    expect(resolveLocator(loc, 'bravo')).toBe(el)
  })

  test('uses nth-of-type when no id is available', () => {
    document.body.innerHTML = `<ul><li>one</li><li>two</li><li>three</li></ul>`
    const second = document.querySelectorAll('li')[1] as HTMLElement
    const loc = buildLocator(second)
    expect(loc).toContain('li:nth-of-type(2)')
    expect(resolveLocator(loc, 'two')).toBe(second)
  })

  test('falls back to tag+text when the structural path no longer matches', () => {
    document.body.innerHTML = `<div><span>find-me</span></div>`
    const span = document.querySelector('span') as HTMLElement
    const loc = buildLocator(span)
    // Mutate the DOM so the nth-of-type path is stale, but the text remains.
    document.body.innerHTML = `<article><em>noise</em></article><aside><span>find-me</span></aside>`
    const resolved = resolveLocator(loc, 'find-me')
    expect(resolved).not.toBeNull()
    expect(resolved?.textContent).toBe('find-me')
  })

  test('returns null when neither path nor text can resolve', () => {
    document.body.innerHTML = `<div><span>gone</span></div>`
    const loc = buildLocator(document.querySelector('span') as HTMLElement)
    document.body.innerHTML = `<div><b>totally different</b></div>`
    expect(resolveLocator(loc, 'gone')).toBeNull()
  })

  test('never resolves onto Parlay UI even if the path would match', () => {
    document.body.innerHTML = `<div id="pa-drawer"><p>chat line</p></div>`
    const p = document.querySelector('#pa-drawer p') as HTMLElement
    const loc = buildLocator(p)
    expect(resolveLocator(loc, 'chat line')).toBeNull()
  })

  test('buildLocator never throws on odd input', () => {
    const detached = document.createElement('div')  // not in document
    expect(() => buildLocator(detached)).not.toThrow()
    expect(() => resolveLocator('<<not a selector', 'x')).not.toThrow()
  })
})

// ── Persist → rehydrate round-trip ────────────────────────────────────────────
describe('persist + initAnnotationPersistence', () => {
  test('persisted annotations rehydrate with el resolved and a marker re-added', () => {
    document.body.innerHTML = `<section><button id="b1">Save</button></section>`
    const el = document.getElementById('b1') as HTMLElement
    annotations.push({ elementText: 'Save', note: 'confirm copy', el })
    persistAnnotations()

    // Simulate reload: clear the in-memory array, keep the DOM + storage.
    annotations.length = 0
    initAnnotationPersistence()

    expect(annotations.length).toBe(1)
    expect(annotations[0].note).toBe('confirm copy')
    expect(annotations[0].el).toBe(el)
    expect(markerMap.has(el)).toBe(true)          // marker re-added
    expect(rerenderCalls).toBe(1)                  // strip refreshed once
  })

  test('an unresolvable annotation keeps its note WITHOUT a marker', () => {
    document.body.innerHTML = `<p>keeper</p>`
    const el = document.querySelector('p') as HTMLElement
    annotations.push({ elementText: 'keeper', note: 'still here', el })
    persistAnnotations()

    annotations.length = 0
    // Reload into a DOM where the element is gone and no text matches.
    document.body.innerHTML = `<div>unrelated content</div>`
    initAnnotationPersistence()

    expect(annotations.length).toBe(1)
    expect(annotations[0].note).toBe('still here')
    expect(annotations[0].el).toBeUndefined()      // no marker target
    expect(document.querySelectorAll('.pa-ann-marker').length).toBe(0)
  })

  test('storage is keyed per page — a record under another page key is not read', () => {
    // happy-dom pins location.pathname, so we assert the keying contract at the
    // storage layer: this page persists under `pa-annotations:<pathname>`, and a
    // record parked under a DIFFERENT page key is invisible to this page.
    document.body.innerHTML = `<p id="x">hi</p>`
    annotations.push({ elementText: 'hi', note: 'mine', el: document.getElementById('x')! })
    persistAnnotations()

    // The stored key is scoped to this page's pathname.
    const thisKey = 'pa-annotations:' + (location.pathname || '/')
    expect(localStorage.getItem(thisKey)).not.toBeNull()

    // A foreign page's saved set must not bleed into this page on load.
    localStorage.setItem(
      'pa-annotations:/some-other-page',
      JSON.stringify([{ elementText: 'foreign', note: 'leak?', locator: 'body > p:nth-of-type(1)' }]),
    )
    annotations.length = 0
    initAnnotationPersistence()
    expect(annotations.every((a) => a.note !== 'leak?')).toBe(true)
    expect(annotations.map((a) => a.note)).toEqual(['mine'])
  })

  test('init on a page with no saved set is a no-op that still leaves the array empty', () => {
    initAnnotationPersistence()
    expect(annotations.length).toBe(0)
  })
})

// ── Clear + resilience ────────────────────────────────────────────────────────
describe('clearPersistedAnnotations + resilience', () => {
  test('clear empties the saved set so a reload rehydrates nothing', () => {
    document.body.innerHTML = `<p id="y">bye</p>`
    annotations.push({ elementText: 'bye', note: 'n', el: document.getElementById('y')! })
    persistAnnotations()
    clearPersistedAnnotations()

    annotations.length = 0
    initAnnotationPersistence()
    expect(annotations.length).toBe(0)
  })

  test('persist with an empty array removes the stored key', () => {
    document.body.innerHTML = `<p id="z">t</p>`
    annotations.push({ elementText: 't', note: 'n', el: document.getElementById('z')! })
    persistAnnotations()
    annotations.length = 0
    persistAnnotations()   // now empty → should remove the key

    initAnnotationPersistence()
    expect(annotations.length).toBe(0)
  })

  test('initAnnotationPersistence never throws on a corrupt stored payload', () => {
    try { localStorage.setItem('pa-annotations:' + location.pathname, '{not json') } catch { /* ignore */ }
    expect(() => initAnnotationPersistence()).not.toThrow()
    expect(annotations.length).toBe(0)
  })

  test('persistAnnotations never throws even if localStorage.setItem throws', () => {
    document.body.innerHTML = `<p id="q">t</p>`
    annotations.push({ elementText: 't', note: 'n', el: document.getElementById('q')! })
    const orig = localStorage.setItem
    localStorage.setItem = () => { throw new Error('quota') }
    try {
      expect(() => persistAnnotations()).not.toThrow()
    } finally {
      localStorage.setItem = orig
    }
  })

  test('a stored record with a non-el (already-rehydrated) annotation keeps its original locator', () => {
    // Simulate an annotation that was rehydrated without an el (locator kept)
    // then re-persisted — its locator must survive so it is not lost.
    const ann: Annotation = { elementText: 'ghost', note: 'kept', locator: 'body > p:nth-of-type(9)' }
    annotations.push(ann)
    persistAnnotations()
    annotations.length = 0
    document.body.innerHTML = ''
    initAnnotationPersistence()
    expect(annotations.length).toBe(1)
    expect(annotations[0].note).toBe('kept')
    expect(annotations[0].locator).toBe('body > p:nth-of-type(9)')
  })
})
