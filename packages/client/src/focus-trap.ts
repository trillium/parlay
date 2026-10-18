// ── Modal focus containment ──────────────────────────────────────────────────
// A dialog marked aria-modal="true" promises assistive tech that everything
// outside it is irrelevant — but the promise is one-sided: nothing stops the
// keyboard. Without a Tab handler, Tab walks straight out of the modal into
// the page behind it, leaving keyboard focus on content the screen reader has
// already been told to ignore (the classic "focus is nowhere" report). The fix
// is to wrap Tab at both ends of the modal's own focusables.
//
// The listener lives on document, not on the overlay: focus that has already
// escaped the modal (a stray click, another component calling .focus()) can
// no longer produce an event on the overlay, so an overlay-local handler could
// never pull it back. trapTabKey(overlay) attaches that handler and returns a
// teardown; callers should invoke it on close rather than relying on the
// overlay being removed.

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

// No visibility filter on purpose: the modals that use this trap contain
// exactly two focusables (a close button and the text field), and a visibility
// test is the one thing here that would behave differently in a headless DOM
// than in a browser.
function focusables(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)]
}

function trap(container: HTMLElement): (e: KeyboardEvent) => void {
  return (e: KeyboardEvent) => {
    if (e.key !== 'Tab') return
    // A teardown that never ran would otherwise swallow Tab for the whole page.
    if (!container.isConnected) return
    const items = focusables(container)
    if (items.length === 0) { e.preventDefault(); return }
    const first = items[0]
    const last = items[items.length - 1]
    const active = document.activeElement as HTMLElement | null
    // Focus escaped (or never entered) the modal — pull it back to an end.
    if (!active || !container.contains(active)) {
      e.preventDefault()
      ;(e.shiftKey ? last : first).focus()
      return
    }
    if (e.shiftKey && active === first) { e.preventDefault(); last.focus() }
    else if (!e.shiftKey && active === last) { e.preventDefault(); first.focus() }
  }
}

export function trapTabKey(overlay: HTMLElement): () => void {
  const onKeydown = trap(overlay)
  document.addEventListener('keydown', onKeydown)
  return () => document.removeEventListener('keydown', onKeydown)
}
