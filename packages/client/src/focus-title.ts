/**
 * focus-title.ts — stamp a [focus:<tag>] marker onto the host page's
 * document.title while a tracked Parlay element holds focus.
 *
 * Why: the panel injects into the host page, so document.title IS the OS window
 * title. Prefixing a machine-readable marker lets an external watcher (Talon,
 * a window-title poller) know where the user's focus lives without any DOM
 * access — voice-first context routing straight off the window title.
 *
 * Robustness: we never cache the host's "real" title (the host may rewrite it
 * live). Every update strips our own leading marker first, then re-adds it, so
 * the operation is idempotent and always reveals the current host title on blur.
 */

// Matches a single leading `[focus:<tag>] ` marker (our own), tag = kebab/underscore slug.
const MARKER_RE = /^\[focus:[a-z0-9][a-z0-9_-]*\]\s+/i

// Strip any marker WE added, revealing the host page's current title.
function baseTitle(): string {
  return document.title.replace(MARKER_RE, '')
}

// Currently-stamped tag, so a focus that moves directly between two tracked
// elements replaces cleanly and a blur only clears when it owns the marker.
let currentTag: string | null = null

function stamp(tag: string): void {
  currentTag = tag
  document.title = `[focus:${tag}] ${baseTitle()}`
}

function clear(tag: string): void {
  // Only clear if we still own the marker for this tag — guards against a
  // focusout firing after a newer focusin already re-stamped a different tag.
  if (currentTag !== tag) return
  currentTag = null
  document.title = baseTitle()
}

/**
 * Track an element: while it (or any descendant) holds focus, the window title
 * carries `[focus:<tag>]`. Uses focusin/focusout (they bubble, unlike focus/blur)
 * so focus landing on a child still counts.
 */
export function trackFocusTitle(el: HTMLElement, tag: string): void {
  el.addEventListener('focusin',  () => stamp(tag))
  el.addEventListener('focusout', (e) => {
    // If focus is moving to another node still inside this element, keep the tag.
    const next = (e as FocusEvent).relatedTarget as Node | null
    if (next && el.contains(next)) return
    clear(tag)
  })
}
