import { esc } from './config'
import { annotations, hoverEl, annotateTarget, markerMap, setHoverEl, setAnnotateTarget, setAnnotate } from './state'
import { openAnnotateMenu, wireAnnotateMenu } from './annotate-menu'
import { wireAnnotationStore, persistAnnotations, clearPersistedAnnotations } from './annotation-store'

// openDrawer and sendMsg injected at wire time to avoid circular deps
let _openDrawer: (() => void) | null = null
let _sendMsg: ((text: string) => Promise<void>) | null = null
let _annToggle: HTMLElement, _annStrip: HTMLElement, _annCount: HTMLElement
let _annList: HTMLElement, _annSend: HTMLElement
let _renderStrip: (() => void) | null = null   // set in setupAnnotation; lets doSetAnnotate refresh the strip
let _popup: HTMLElement, _popupLbl: HTMLElement
let _popupIn: HTMLTextAreaElement, _popupOk: HTMLElement, _popupCx: HTMLElement

// Per-message annotation entry point — assigned inside wireAnnotation (same
// closure pattern as _renderStrip). Lets thread.ts open the annotation popup
// pre-targeted at a specific chat reply without arming page-annotate mode.
let _annotateEl: ((el: HTMLElement, x: number, y: number) => void) | null = null
export function annotateMessage(el: HTMLElement, x: number, y: number) {
  _annotateEl?.(el, x, y)
}

export function wireAnnotation(
  annToggle: HTMLElement, annStrip: HTMLElement, annCount: HTMLElement,
  annList: HTMLElement, annSend: HTMLElement,
  popup: HTMLElement, popupLbl: HTMLElement, popupIn: HTMLTextAreaElement,
  popupOk: HTMLElement, popupCx: HTMLElement,
  openDrawer: () => void,
  sendMsg: (text: string) => Promise<void>,
) {
  _annToggle = annToggle; _annStrip = annStrip; _annCount = annCount
  _annList = annList; _annSend = annSend
  _popup = popup; _popupLbl = popupLbl; _popupIn = popupIn
  _popupOk = popupOk; _popupCx = popupCx
  _openDrawer = openDrawer; _sendMsg = sendMsg

  function isSkipped(el: Element | null): boolean {
    if (!el || el === document.body || el === document.documentElement) return true
    return !!(el.closest('#pa-drawer') || el.closest('#pa-trigger') || el.closest('#pa-popup'))
  }

  function hidePopup() { _popup.classList.remove('visible'); setAnnotateTarget(null) }

  function showPopup(el: HTMLElement, x: number, y: number) {
    const label = (el.textContent || el.getAttribute('title') || el.tagName || 'element').trim().slice(0, 60)
    _popupLbl.textContent = `↗ "${label}"`
    _popupIn.value = ''
    const W = window.innerWidth, H = window.innerHeight
    let left = x + 10, top = y + 10
    if (left + 270 > W) left = x - 280
    if (top + 160 > H) top = y - 170
    _popup.style.left = Math.max(4, left) + 'px'
    _popup.style.top  = Math.max(4, top)  + 'px'
    _popup.classList.add('visible')
    setTimeout(() => _popupIn.focus(), 60)
  }

  _annotateEl = (el, x, y) => {
    setAnnotateTarget(el)
    showPopup(el, x, y)
  }

  function addMarker(el: HTMLElement, num: number) {
    if (markerMap.has(el)) return
    const m = document.createElement('div')
    m.className = 'pa-ann-marker'
    m.textContent = String(num)
    const saved = el.style.position
    if (!saved || saved === 'static') el.style.position = 'relative'
    m.style.cssText = 'position:absolute;top:-9px;right:-9px;'
    el.appendChild(m)
    markerMap.set(el, m)
  }

  _renderStrip = renderAnnStrip
  function renderAnnStrip() {
    const active = _annToggle.classList.contains('active')
    // Show the strip whenever annotate mode is armed — even with nothing marked
    // yet — so there is always a visible way out. Hide only when idle + empty.
    if (!annotations.length && !active) { _annStrip.classList.remove('visible'); return }
    _annStrip.classList.add('visible')
    _annStrip.classList.toggle('empty', annotations.length === 0)
    _annCount.textContent = String(annotations.length)
    _annList.innerHTML = annotations.map((a, i) => `
      <div class="pa-ann-item">
        <span class="pa-ann-num">${i + 1}</span>
        <div style="flex:1;min-width:0">
          <div class="pa-ann-el">${esc(a.elementText)}</div>
          <div class="pa-ann-text">${esc(a.note)}</div>
        </div>
        <button class="pa-ann-rm" data-i="${i}">✕</button>
      </div>`).join('')
    _annList.querySelectorAll('.pa-ann-rm').forEach(btn => {
      btn.addEventListener('click', () => {
        const i = Number((btn as HTMLElement).dataset.i)
        const a = annotations[i]
        if (a?.el && markerMap.has(a.el)) { markerMap.get(a.el)!.remove(); markerMap.delete(a.el) }
        annotations.splice(i, 1)
        persistAnnotations()   // survive reload: mirror the removal to storage
        renderAnnStrip()
      })
    })
  }

  function confirmAnnotation() {
    const note = _popupIn.value.trim()
    if (!note || !annotateTarget) { hidePopup(); return }
    const el = annotateTarget
    const elementText = (el.textContent || el.getAttribute('title') || el.tagName || 'element').trim().slice(0, 80)
    annotations.push({ elementText, note, el })
    addMarker(el, annotations.length)
    persistAnnotations()   // survive reload: mirror the new annotation to storage
    hidePopup()
    renderAnnStrip()
    // Deliberately do NOT open the side drawer here: the annotation strip
    // (count + Done button) already stays visible, so the user sees queued
    // annotations without losing full-width working room on the page. The
    // drawer opens only when the user chooses to (send / trigger).
  }

  // First click arms annotate mode. Second click (while active) opens the menu
  // to choose routing, give feedback, or close. Arming leaves the page full-width;
  // the strip (not the side drawer) is the queued-annotations surface.
  wireAnnotateMenu(() => doSetAnnotate(false))
  _annToggle.addEventListener('click', () => {
    if (_annToggle.classList.contains('active')) { openAnnotateMenu(_annToggle); return }
    doSetAnnotate(true)
  })

  // Explicit exit affordances: the strip's Done button and a global Escape key.
  const annExit = document.getElementById('pa-ann-exit')
  if (annExit) annExit.addEventListener('click', () => doSetAnnotate(false))
  document.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key !== 'Escape' || !_annToggle.classList.contains('active')) return
    if (_popup.classList.contains('visible')) { hidePopup(); return }  // close popup first
    doSetAnnotate(false)
  })

  document.addEventListener('mousemove', (e: MouseEvent) => {
    if (!_annToggle.classList.contains('active')) return
    const el = e.target as HTMLElement
    if (el === hoverEl) return
    if (hoverEl) hoverEl.classList.remove('pa-hover')
    if (!isSkipped(el)) { el.classList.add('pa-hover'); setHoverEl(el) } else { setHoverEl(null) }
  }, { passive: true })

  document.addEventListener('click', (e: MouseEvent) => {
    if (!_annToggle.classList.contains('active') || isSkipped(e.target as Element) || (e.target as Element).closest('#pa-popup')) return
    e.preventDefault(); e.stopPropagation()
    setAnnotateTarget(e.target as HTMLElement)
    showPopup(e.target as HTMLElement, e.clientX, e.clientY)
  }, true)

  _popupCx.addEventListener('click', hidePopup)
  _popupIn.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key === 'Escape') hidePopup()
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') { e.preventDefault(); confirmAnnotation() }
  })
  _popupOk.addEventListener('click', confirmAnnotation)

  _annSend.addEventListener('click', () => { void sendAnnotations() })

  // Hand the persistence layer the two closures it needs: a way to refresh the
  // strip after rehydration, and a way to re-add a marker for a resolved target.
  // initAnnotationPersistence() (called from init.ts after wireAnnotation) uses
  // these to repopulate the strip from storage on load.
  wireAnnotationStore(renderAnnStrip, addMarker)
}

export async function sendAnnotations(): Promise<void> {
  if (!annotations.length || !_sendMsg) return
  const page = document.title || location.pathname
  const lines = annotations.map((a, i) => `${i + 1}. [${a.elementText}]: ${a.note}`).join('\n')
  annotations.forEach(a => {
    if (a.el && markerMap.has(a.el)) { markerMap.get(a.el)!.remove(); markerMap.delete(a.el) }
  })
  annotations.length = 0
  clearPersistedAnnotations()   // sent annotations must not resurrect on reload
  _renderStrip?.()
  await _sendMsg(`ANNOTATIONS on "${page}":\n${lines}`)
}

export function doSetAnnotate(on: boolean) {
  if (!_annToggle) return
  setAnnotate(on)
  _annToggle.classList.toggle('active', on)
  _annToggle.title = on ? 'Exit annotate mode (Esc)' : 'Annotate page'
  document.body.style.cursor = on ? 'crosshair' : ''
  if (!on && hoverEl) { hoverEl.classList.remove('pa-hover'); setHoverEl(null) }
  if (!on && _popup) _popup.classList.remove('visible')
  _renderStrip?.()   // show/hide the strip (with its Done button) as mode toggles
}
