import { esc } from './config'
import { agentInfo, activeChannel, unreadByChannel } from './state'
import { archived, unarchiveChannel, switchChannel, statusOf } from './tabs'

// ── Mobile agent switcher: floating button above the input → tap-friendly sheet ──

function sheetEl() { return document.getElementById('pa-sheet') }
export function sheetOpen() { return !!sheetEl()?.classList.contains('open') }

export function renderSheet() {
  const list = document.getElementById('pa-sheet-list')
  if (!list) return
  list.innerHTML = ''
  const entries = [...agentInfo.entries()].sort(([a], [b]) => Number(archived.has(a)) - Number(archived.has(b)))
  for (const [id, info] of entries) {
    const row = document.createElement('button')
    row.className = 'pa-sheet-row' + (activeChannel === id ? ' active' : '')
    row.style.setProperty('--tab-color', info.color || 'var(--pa-green)')
    const count = unreadByChannel[id] || 0
    row.innerHTML = `<span class="pa-tab-pip ${statusOf(id)}"></span><span class="pa-sheet-name">${esc(info.name)}</span><span class="pa-sheet-id">${esc(id)}${archived.has(id) ? ' · archived' : ''}</span>${count ? `<span class="pa-tab-unread visible" style="position:static">${count}</span>` : ''}`
    row.addEventListener('click', () => {
      if (archived.has(id)) unarchiveChannel(id)
      switchChannel(id)
      sheetEl()?.classList.remove('open')
    })
    list.appendChild(row)
  }
}

export function initAgentSwitcher() {
  const fab = document.getElementById('pa-fab')
  const sheet = sheetEl()
  if (!fab || !sheet) return
  fab.addEventListener('click', (e) => {
    e.stopPropagation()
    const opening = !sheet.classList.contains('open')
    if (opening) renderSheet()
    sheet.classList.toggle('open')
    if (opening) {
      document.addEventListener('click', (ev) => {
        if (!sheet.contains(ev.target as Node)) sheet.classList.remove('open')
      }, { once: true })
    }
  })
  document.getElementById('pa-sheet-close')?.addEventListener('click', () => sheet.classList.remove('open'))
  // Control shortcuts: proxy the header buttons so all existing behavior carries over
  for (const btn of sheet.querySelectorAll<HTMLElement>('.pa-sheet-act')) {
    btn.addEventListener('click', () => {
      document.getElementById(btn.dataset.proxy || '')?.click()
      sheet.classList.remove('open')
    })
  }
}
