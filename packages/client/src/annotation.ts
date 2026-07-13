import { esc } from './config'
import { annotations, hoverEl, annotateTarget, markerMap, setHoverEl, setAnnotateTarget, setAnnotate } from './state'

// openDrawer and sendMsg injected at wire time to avoid circular deps
let _openDrawer: (() => void) | null = null
let _sendMsg: ((text: string) => Promise<void>) | null = null
let _annToggle: HTMLElement, _annStrip: HTMLElement, _annCount: HTMLElement
let _annList: HTMLElement, _annSend: HTMLElement
let _popup: HTMLElement, _popupLbl: HTMLElement
let _popupIn: HTMLTextAreaElement, _popupOk: HTMLElement, _popupCx: HTMLElement

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

  function renderAnnStrip() {
    if (!annotations.length) { _annStrip.classList.remove('visible'); return }
    _annStrip.classList.add('visible')
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
    hidePopup()
    renderAnnStrip()
    if (_openDrawer) _openDrawer()
  }

  _annToggle.addEventListener('click', () => {
    const isActive = _annToggle.classList.contains('active')
    doSetAnnotate(!isActive)
    if (!isActive && _openDrawer) _openDrawer()
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

  _annSend.addEventListener('click', async () => {
    if (!annotations.length || !_sendMsg) return
    const page = document.title || location.pathname
    const lines = annotations.map((a, i) => `${i + 1}. [${a.elementText}]: ${a.note}`).join('\n')
    annotations.forEach(a => {
      if (a.el && markerMap.has(a.el)) { markerMap.get(a.el)!.remove(); markerMap.delete(a.el) }
    })
    annotations.length = 0
    renderAnnStrip()
    await _sendMsg(`ANNOTATIONS on "${page}":\n${lines}`)
  })
}

export function doSetAnnotate(on: boolean) {
  if (!_annToggle) return
  setAnnotate(on)
  _annToggle.classList.toggle('active', on)
  document.body.style.cursor = on ? 'crosshair' : ''
  if (!on && hoverEl) { hoverEl.classList.remove('pa-hover'); setHoverEl(null) }
  if (!on && _popup) _popup.classList.remove('visible')
}
