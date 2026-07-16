import { esc } from './config'
import { agentInfo, activeChannel, unreadByChannel, channelStatus, lastActivityByChannel } from './state'
import { archived, unarchiveChannel, switchChannel, statusOf } from './tabs'

// ── Mobile agent switcher: floating button above the input → tap-friendly sheet ──

function sheetEl() { return document.getElementById('pa-sheet') }
export function sheetOpen() { return !!sheetEl()?.classList.contains('open') }

export function renderSheet() {
  const list = document.getElementById('pa-sheet-list')
  if (!list) return
  list.innerHTML = ''
  // Mirror the tab panel sort: recent-activity first, then listening, then alpha; archived last; system always last.
  const entries = [...agentInfo.entries()].sort(([a, ai], [b, bi]) => {
    const aArch = archived.has(a), bArch = archived.has(b)
    if (aArch !== bArch) return Number(aArch) - Number(bArch)
    const aSys = a === 'system', bSys = b === 'system'
    if (aSys !== bSys) return Number(aSys) - Number(bSys)
    const aTime = lastActivityByChannel[a] ?? 0, bTime = lastActivityByChannel[b] ?? 0
    if (bTime !== aTime) return bTime - aTime
    const aListen = channelStatus[a] === 'listening' ? 1 : 0
    const bListen = channelStatus[b] === 'listening' ? 1 : 0
    if (bListen !== aListen) return bListen - aListen
    return (ai.nicknames?.[0] ?? ai.name).localeCompare(bi.nicknames?.[0] ?? bi.name)
  })
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

// Mirror each proxy target's active state onto its sheet button (e.g. TTS on)
function syncSheetActions(sheet: HTMLElement) {
  for (const btn of sheet.querySelectorAll<HTMLElement>('.pa-sheet-act')) {
    const target = document.getElementById(btn.dataset.proxy || '')
    btn.classList.toggle('active', !!target?.classList.contains('active'))
  }
}

export function initAgentSwitcher() {
  const fab = document.getElementById('pa-fab')
  const sheet = sheetEl()
  if (!fab || !sheet) return
  fab.addEventListener('click', (e) => {
    e.stopPropagation()
    const opening = !sheet.classList.contains('open')
    if (opening) { renderSheet(); syncSheetActions(sheet) }
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
      syncSheetActions(sheet)
      sheet.classList.remove('open')
    })
  }
}
