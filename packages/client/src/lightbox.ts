// ── Image lightbox (#17 amendment) ───────────────────────────────────────────
// Full-screen in-panel overlay for any rendered image. Delegated on .pa-img
// clicks, so every current and future image surface (chat thumbnails, glance
// captures) gets it with zero extra wiring. Close: ✕, backdrop tap, Escape.
// Double-tap/double-click toggles 2.5× zoom at the tap point; native
// long-press/context menu still offers open-in-new-tab for free.

let overlay: HTMLElement | null = null

export function openLightbox(src: string) {
  closeLightbox()
  overlay = document.createElement('div')
  overlay.id = 'pa-lightbox'
  overlay.innerHTML = `
    <button id="pa-lightbox-x" title="Close (Esc)">✕</button>
    <img src="${src.replace(/"/g, '%22')}" alt="">`
  document.body.appendChild(overlay)
  const img = overlay.querySelector('img') as HTMLImageElement

  overlay.addEventListener('click', (e) => { if (e.target === overlay) closeLightbox() })
  overlay.querySelector('#pa-lightbox-x')!.addEventListener('click', closeLightbox)

  // Double-tap / double-click zoom toggle, origin at the tap point
  let zoomed = false
  const toggleZoom = (x: number, y: number) => {
    zoomed = !zoomed
    const r = img.getBoundingClientRect()
    img.style.transformOrigin = `${((x - r.left) / r.width) * 100}% ${((y - r.top) / r.height) * 100}%`
    img.style.transform = zoomed ? 'scale(2.5)' : ''
    img.classList.toggle('zoomed', zoomed)
  }
  img.addEventListener('dblclick', (e) => { e.preventDefault(); toggleZoom(e.clientX, e.clientY) })
  let lastTap = 0
  img.addEventListener('touchend', (e) => {
    const now = performance.now()
    if (now - lastTap < 300 && e.changedTouches[0]) {
      e.preventDefault()
      toggleZoom(e.changedTouches[0].clientX, e.changedTouches[0].clientY)
    }
    lastTap = now
  })
}

export function closeLightbox() {
  overlay?.remove()
  overlay = null
}

export function initLightbox() {
  document.addEventListener('click', (e) => {
    const img = (e.target as HTMLElement).closest?.('.pa-img')
    if (!img) return
    e.preventDefault()
    e.stopPropagation()
    openLightbox((img as HTMLImageElement).src)
  }, true)
  document.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key === 'Escape' && overlay) { e.stopPropagation(); closeLightbox() }
  }, true)
}
