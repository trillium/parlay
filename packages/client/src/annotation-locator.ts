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
//      path's leaf and whose trimmed text starts with the saved elementText. A
//      second, weaker pass accepts a candidate whose text is a *prefix* of the
//      saved label (live text can shrink — streaming output, a removed child),
//      but only for a substantial prefix: see MIN_PREFIX_MATCH below.
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
  return scanByText(leafTagOf(locator), wanted)
}

// Shortest candidate text the weaker reverse pass will accept. Without a floor,
// ANY element whose text happens to be a prefix of the label matches — the
// first single character in document order — so a stale annotation on the
// button "Logout" re-anchors onto the site's one-letter logo link. Below this
// floor the resolver returns null and the caller keeps the note markerless,
// which is the safe outcome: a missing marker beats a marker on the wrong
// element.
const MIN_PREFIX_MATCH = 4

// Find a replacement target by text among elements of `leafTag` (or any element
// when the leaf was an id segment and carries no tag). Two ordered passes:
//
//   a) forward — candidate text starts with the saved label (the documented
//                contract, and the only pass that can match a full label);
//   b) reverse — candidate text is a prefix of the saved label, i.e. the
//                element's live text has shrunk since the annotation was made.
//                Consulted only when (a) found nothing, so a shortened
//                candidate never outranks a real match appearing later in the
//                document.
function scanByText(leafTag: string, wanted: string): HTMLElement | null {
  let nodes: ArrayLike<Element>
  try {
    nodes = leafTag ? document.getElementsByTagName(leafTag) : document.querySelectorAll('*')
  } catch {
    return null
  }
  const candidates = Array.from(nodes).filter(
    (n): n is HTMLElement => n instanceof HTMLElement && !isParlayUi(n),
  )
  for (const node of candidates) {
    if ((node.textContent || '').trim().startsWith(wanted)) return node
  }
  for (const node of candidates) {
    const text = (node.textContent || '').trim()
    if (text.length >= MIN_PREFIX_MATCH && wanted.startsWith(text)) return node
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
