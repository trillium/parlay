// Annotation persistence — survives page reloads.
//
// Page annotations live only in the in-memory `annotations` array (state.ts)
// with live DOM `el` references, so a reload wipes them along with their visual
// markers. This module makes them durable:
//   - a serializable per-page record (note + `elementText` + a `locator` that
//     can re-find the target element on a fresh DOM), keyed by page identity
//   - persist-on-change (persistAnnotations / clearPersistedAnnotations, called
//     from annotation.ts confirm/remove/clear)
//   - rehydrate-on-load (initAnnotationPersistence, called at startup)
//
// STORAGE CHOICE: localStorage, keyed per page. idb.ts uses IndexedDB because it
// caches up to 1000 chat messages and needs indexed range pruning. Annotations
// are a handful per page and read exactly once at startup, so the synchronous,
// zero-ceremony localStorage API is the better fit — no open()/transaction
// dance, and "never blocks render" is automatic (a few small string writes).
// Every access is wrapped in try/catch so a disabled/again-full store, a foreign
// origin, or a serialization error can never break the page.

import { annotations, markerMap, type Annotation } from './state'
import { buildLocator, resolveLocator } from './annotation-locator'

const KEY_PREFIX = 'pa-annotations:'

// Re-render + marker-add hooks, injected by annotation.ts at wire time so this
// module owns storage while annotation.ts owns the DOM/render logic. Kept as
// nullable function refs to avoid a circular import at module-eval time.
let _rerenderStrip: (() => void) | null = null
let _addMarker: ((el: HTMLElement, num: number) => void) | null = null

export function wireAnnotationStore(
  rerenderStrip: () => void,
  addMarker: (el: HTMLElement, num: number) => void,
): void {
  _rerenderStrip = rerenderStrip
  _addMarker = addMarker
}

// Serializable form of one annotation. `el` is deliberately dropped — a live DOM
// node cannot be stored — and re-derived from `locator` on rehydrate.
interface StoredAnnotation {
  elementText: string
  note:        string
  locator:     string
}

// Page identity: the pathname is the stable per-page key. A proxied Lavish page
// carries its session key in the path (…/lavish-proxy/session/<key>), so the
// pathname already distinguishes pages that share an origin. Search/hash are
// excluded so transient query state does not fragment a page's saved set.
function pageKey(): string {
  try {
    return KEY_PREFIX + (location.pathname || '/')
  } catch {
    return KEY_PREFIX + '/'
  }
}

function readStored(): StoredAnnotation[] {
  try {
    const raw = localStorage.getItem(pageKey())
    if (!raw) return []
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    // Defensively keep only well-shaped records.
    return parsed.filter(
      (r): r is StoredAnnotation =>
        !!r &&
        typeof r.elementText === 'string' &&
        typeof r.note === 'string' &&
        typeof r.locator === 'string',
    )
  } catch {
    return []
  }
}

// Serialize the current in-memory `annotations` array to this page's slot.
// Annotations whose `el` is present get a freshly computed locator; a rehydrated
// annotation that never resolved keeps its original stored locator so it is not
// lost on the next save. Fire-and-forget: never throws, never blocks.
export function persistAnnotations(): void {
  try {
    const records: StoredAnnotation[] = []
    for (const a of annotations) {
      const locator = a.el ? buildLocator(a.el) : a.locator
      if (!locator) continue
      records.push({ elementText: a.elementText, note: a.note, locator })
    }
    const key = pageKey()
    if (records.length === 0) {
      localStorage.removeItem(key)
    } else {
      localStorage.setItem(key, JSON.stringify(records))
    }
  } catch {
    // Storage disabled/full/foreign-origin — persistence is best-effort.
  }
}

// Drop this page's entire saved set. Called when `sendAnnotations` empties the
// in-memory array — sent annotations should not resurrect on the next reload.
export function clearPersistedAnnotations(): void {
  try {
    localStorage.removeItem(pageKey())
  } catch {
    // best-effort
  }
}

// Rehydrate this page's saved annotations into the in-memory array on load.
// Each record's locator is re-resolved against the fresh DOM (best-effort):
//   - resolved  → push with `el` set + re-add its marker
//   - unresolved → push WITHOUT `el` so the note still shows in the strip,
//                  just without a page marker (never dropped, never crashes)
// Then refresh the strip UI via the injected re-render hook.
export function initAnnotationPersistence(): void {
  try {
    const stored = readStored()
    if (stored.length === 0) return

    for (const rec of stored) {
      let el: HTMLElement | undefined
      try {
        el = resolveLocator(rec.locator, rec.elementText) ?? undefined
      } catch {
        el = undefined
      }
      const ann: Annotation = {
        elementText: rec.elementText,
        note: rec.note,
        locator: rec.locator,
        el,
      }
      annotations.push(ann)
      if (el && _addMarker && !markerMap.has(el)) {
        try {
          _addMarker(el, annotations.length)
        } catch {
          // A marker failure must not abort rehydration of the remaining notes.
        }
      }
    }
  } catch {
    // Any unexpected failure leaves the page working with zero rehydrated
    // annotations rather than crashing the drawer bootstrap.
  } finally {
    try {
      _rerenderStrip?.()
    } catch {
      // render is best-effort too
    }
  }
}
