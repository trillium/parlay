import { esc, CHAT_BASE } from './config'
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
let _popupAttachments: string[] = []

// Per-message annotation entry point — assigned inside wireAnnotation (same
// closure pattern as _renderStrip). Lets thread.ts open the annotation popup
// pre-targeted at a specific chat reply without arming page-annotate mode.
let _annotateEl: ((el: HTMLElement, x: number, y: number) => void) | null = null
export function annotateMessage(el: HTMLElement, x: number, y: number) {
  _annotateEl?.(el, x, y)
}

async function uploadFile(file: File): Promise<string | null> {
  try {
    const form = new FormData()
    form.append('file', file)
    const r = await fetch(`${CHAT_BASE}/upload`, { method: 'POST', body: form })
    const res = await r.json()
    return res.ok && res.url ? res.url : null
  } catch { return null }
}

function renderPopupAttachments() {
  let attachStrip = _popup.querySelector('.pa-popup-attachments') as HTMLElement | null
  if (!attachStrip && _popupAttachments.length > 0) {
    attachStrip = document.createElement('div')
    attachStrip.className = 'pa-popup-attachments'
    attachStrip.style.cssText = 'display:flex;gap:6px;margin-bottom:8px;flex-wrap:wrap'
    _popupIn.parentNode?.insertBefore(attachStrip, _popupIn)
  }
  if (!attachStrip) return
  attachStrip.innerHTML = _popupAttachments.map((url, i) =>
    `<div class="pa-popup-chip" style="position:relative;display:inline-block;max-width:80px"><img src="${url.replace(/"/g, '%22')}" alt="" style="max-width:80px;max-height:60px;border-radius:4px"><button class="pa-popup-chip-x" data-i="${i}" style="position:absolute;top:-8px;right:-8px;width:20px;height:20px;padding:0;border-radius:50%;background:#f00;color:#fff;border:none;cursor:pointer;font-size:12px">✕</button></div>`
  ).join('')
  attachStrip.querySelectorAll('.pa-popup-chip-x').forEach(btn => btn.addEventListener('click', () => {
    _popupAttachments.splice(Number((btn as HTMLElement).dataset.i), 1)
    renderPopupAttachments()
  }))
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
    return !!(el.closest('#pa-drawer') || el.closest('#pa-trigger') || el.closest('#pa-popup') || el.closest('#pa-annotate-menu'))
  }

  function hidePopup() { _popup.classList.remove('visible'); setAnnotateTarget(null) }

  function showPopup(el: HTMLElement, x: number, y: number) {
    const label = (el.textContent || el.getAttribute('title') || el.tagName || 'element').trim().slice(0, 60)
    _popupLbl.textContent = `↗ "${label}"`
    _popupIn.value = ''
    _popupAttachments = []
    renderPopupAttachments()
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
    if (!note && !_popupAttachments.length) { hidePopup(); return }
    if (!annotateTarget) { hidePopup(); return }
    const el = annotateTarget
    const elementText = (el.textContent || el.getAttribute('title') || el.tagName || 'element').trim().slice(0, 80)
    // Carry source bead id (nearest data-bead ancestor) so agents resolve by lookup, not text-match (task-mkns).
    const beadEl = el.closest('[data-bead]') as HTMLElement | null
    const bead = beadEl?.dataset.bead || undefined
    annotations.push({
      elementText, note, el,
      ...(bead && { bead }),
      ..._popupAttachments.length && { attachments: [..._popupAttachments] }
    })
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
  _popupIn.addEventListener('paste', (e: ClipboardEvent) => {
    const items = [...(e.clipboardData?.items ?? [])]
    const files = items.filter(i => i.type.startsWith('image/'))
      .map(i => i.getAsFile()).filter((f): f is File => !!f)
    if (!files.length) return
    e.preventDefault()
    void (async () => {
      for (const f of files.slice(0, 4)) {
        const url = await uploadFile(f)
        if (url) { _popupAttachments.push(url); renderPopupAttachments() }
      }
    })()
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
  const lines = annotations.map((a, i) => {
    const head = a.bead ? `${a.bead} | ${a.elementText}` : a.elementText
    const text = `${i + 1}. [${head}]: ${a.note}`
    return a.attachments?.length ? `${text}\n   Attachments: ${a.attachments.join(' ')}` : text
  }).join('\n')
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
