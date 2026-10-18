// Look up an element by an ATTRIBUTE VALUE without interpolating that value
// into a CSS selector. Message ids are server data; `[data-pa-id="${id}"]`
// either rewrites the selector to match a DIFFERENT message or makes it
// unparseable (a DOMException that aborts the one listener frame). Plugin
// bundles cannot import core modules (a second copy of core module state would
// split-brain), so the plugin compares the value itself.
export function firstByAttr(root: ParentNode, attr: string, value: unknown): HTMLElement | null {
  const want = String(value)
  for (const el of root.querySelectorAll<HTMLElement>(`[${attr}]`)) {
    if (el.getAttribute(attr) === want) return el
  }
  return null
}
