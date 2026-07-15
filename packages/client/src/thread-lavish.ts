import { esc } from './config'
import { thread, emptyEl } from './dom'
import { scrollBottom } from './thread-scroll'
import { navigateWorkspace } from './commands/ctx'

// ── Lavish artifact card ──────────────────────────────────────────────────────
// The inline card an agent drops to hand the captain a reviewable artifact page.
// Linking philosophy: relative, workspace-scoped. A normal click navigates via
// navigateWorkspace — on a Parlay shell page (with #pa-workspace) only the
// surface OUTSIDE the panel reloads (panel stays live, zero SSE teardown); on a
// plain page it falls back to a full navigation. Cmd/Ctrl/Shift/middle-click keep
// the native open-in-new-tab, so the relative href still behaves as a real link.

export function insertLavishCard(key: string, file: string, proxyUrl: string, status: string) {
  const name = file.split('/').pop() ?? file
  if (status === 'ended') {
    const el = document.getElementById(`pa-lavish-${key}`)
    if (el) el.classList.add('closed')
    return
  }
  emptyEl.style.display = 'none'
  // Strip the origin so the artifact opens same-origin as a relative link.
  let rel = proxyUrl
  try { const u = new URL(proxyUrl, location.origin); rel = u.pathname + u.search + u.hash } catch { /* keep as-is */ }
  const el = document.createElement('div')
  el.id = `pa-lavish-${key}`
  el.className = 'pa-lavish-card'
  el.innerHTML = `
    <div class="pa-lavish-icon">📄</div>
    <div class="pa-lavish-body">
      <div class="pa-lavish-label">Lavish artifact</div>
      <div class="pa-lavish-name">${esc(name)}</div>
    </div>
    <a class="pa-lavish-btn" href="${esc(rel)}">Open →</a>`
  el.querySelector('.pa-lavish-btn')!.addEventListener('click', (e) => {
    const me = e as MouseEvent
    if (me.metaKey || me.ctrlKey || me.shiftKey || me.button === 1) return
    e.preventDefault()
    navigateWorkspace(rel)
  })
  thread.appendChild(el)
  scrollBottom(true)
}
