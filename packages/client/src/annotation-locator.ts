// Serializable element locators for annotation persistence.
//
// An annotation targets an arbitrary element on a proxied page. To re-find that
// element after a reload (fresh DOM, no retained references) we serialize a
// structural CSS path and, as a fallback, a tag+text heuristic.
//
// Robustness ordering, most→least reliable:
//   1. If any ancestor has a usable plain `id`, anchor the path there and stop
//      climbing — shortest, most stable selector.
//   2. Otherwise walk up to <body>, each hop = `tag:nth-of-type(n)` (position
//      among same-tag siblings) — position-stable across identical re-renders.
//   3. On resolve failure, fall back to the first element whose tag matches the
//      path's leaf and whose trimmed text starts with the saved elementText.
//
// Parlay's own injected UI (#pa-*) is never an annotation target, but the
// resolver still rejects any match inside it so a moved/rebuilt page can never
// re-anchor an annotation onto the drawer.

// A plain id is safe to use as a `#id` anchor only if it is a valid CSS ident
// with no whitespace/special chars that would need escaping.
function usableId(el: Element): string | null {
  const id = el.getAttribute('id')
  if (!id) return null
  if (id.startsWith('pa-')) return null // never anchor on Parlay's own UI
  if (!/^[A-Za-z][\w-]*$/.test(id)) return null
  // Must be unique to be a reliable anchor.
  try {
    if (document.querySelectorAll('#' + id).length !== 1) return null
  } catch {
    return null
  }
  return id
}

// 1-based index of `el` among siblings sharing its tag name.
function nthOfType(el: Element): number {
  let n = 1
  let sib = el.previousElementSibling
  while (sib) {
    if (sib.tagName === el.tagName) n++
    sib = sib.previousElementSibling
  }
  return n
}

// Build a serializable CSS path from `el` up to a stable anchor (an id'd
// ancestor, or <body>). Returns '' if a path cannot be built.
export function buildLocator(el: HTMLElement): string {
  try {
    if (!el || el.nodeType !== 1) return ''
    const segs: string[] = []
    let node: Element | null = el
    while (node && node.nodeType === 1 && node !== document.documentElement) {
      const tag = node.tagName.toLowerCase()
      if (tag === 'body') { segs.unshift('body'); break }
      const id = usableId(node)
      if (id) { segs.unshift('#' + id); break }
      segs.unshift(`${tag}:nth-of-type(${nthOfType(node)})`)
      node = node.parentElement
    }
    return segs.length ? segs.join(' > ') : ''
  } catch {
    return ''
  }
}

// Is `el` inside Parlay's own injected UI? Such a match is never a valid
// annotation target and must be rejected.
function isParlayUi(el: Element): boolean {
  return !!el.closest('#pa-drawer, #pa-trigger, #pa-popup, #pa-ann-strip')
}

// Re-find the target element for a saved locator on the current DOM.
// Returns null when nothing resolves (caller keeps the note markerless).
export function resolveLocator(locator: string, elementText: string): HTMLElement | null {
  // 1. Exact structural path.
  try {
    if (locator) {
      const hit = document.querySelector(locator)
      if (hit instanceof HTMLElement && !isParlayUi(hit)) return hit
    }
  } catch {
    // Malformed selector (e.g. an old record from a changed schema) — fall
    // through to the text heuristic.
  }

  // 2. Tag+text heuristic. Derive the leaf tag from the path, then find the
  // first element of that tag whose trimmed text starts with the saved label.
  const wanted = (elementText || '').trim()
  if (!wanted) return null
  const leafTag = leafTagOf(locator)
  try {
    const scope = leafTag ? document.getElementsByTagName(leafTag) : document.querySelectorAll('*')
    for (const node of Array.from(scope)) {
      if (!(node instanceof HTMLElement)) continue
      if (isParlayUi(node)) continue
      const text = (node.textContent || '').trim()
      if (text && (text.startsWith(wanted) || wanted.startsWith(text))) return node
    }
  } catch {
    return null
  }
  return null
}

// Extract the leaf tag name from a locator path. The leaf segment is either
// `tag:nth-of-type(n)` or `#id`; an id segment carries no tag, so returns ''.
function leafTagOf(locator: string): string {
  if (!locator) return ''
  const leaf = locator.split('>').pop()?.trim() ?? ''
  if (!leaf || leaf.startsWith('#')) return ''
  const tag = leaf.split(':')[0].trim()
  return /^[a-z][a-z0-9-]*$/i.test(tag) ? tag : ''
}
