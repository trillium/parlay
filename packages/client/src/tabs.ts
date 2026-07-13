import { esc, CHAT_BASE } from './config'
import { agentInfo, activeChannel, unreadByChannel, setActiveChannel } from './state'
import { tabsEl, inputEl, connBanner } from './dom'

let _renderThread: (() => void) | null = null
export function setRenderThreadFn(fn: () => void) { _renderThread = fn }

// Archived (stagnant) agent tabs — hidden behind a dropdown, persisted locally.
const ARCHIVED_KEY = 'pa-archived-channels'
const archived = new Set<string>((() => {
  try { return JSON.parse(localStorage.getItem(ARCHIVED_KEY) || '[]') } catch { return [] }
})())
function persistArchived() {
  try { localStorage.setItem(ARCHIVED_KEY, JSON.stringify([...archived])) } catch {}
}
export function archiveChannel(id: string) {
  archived.add(id)
  persistArchived()
  if (activeChannel === id) switchChannel(null)   // re-renders tabs + thread
  else renderTabs()
}
export function unarchiveChannel(id: string) {
  archived.delete(id)
  persistArchived()
}

// Persist the selected tab across refreshes ('' = the All view).
const ACTIVE_KEY = 'pa-active-channel'
let _restored = false
function restoreActiveChannel() {
  if (_restored) return
  _restored = true
  let saved: string | null = null
  try { saved = localStorage.getItem(ACTIVE_KEY) } catch {}
  if (saved === null) return              // nothing saved → keep default
  if (saved === '') { setActiveChannel(null); return }   // explicit All view
  if (agentInfo.has(saved)) setActiveChannel(saved)      // restore only if the tab still exists
}

export function msgInView(m: any): boolean {
  if (activeChannel === null) return true
  if (m.role === 'user') return true
  return !m.channel || m.channel === activeChannel
}

function updateHeader(ch: string | null) {
  const subEl = document.getElementById('pa-sub')
  const dotEl = document.getElementById('pa-dot')
  if (!subEl || !dotEl) return
  if (ch && agentInfo.has(ch)) {
    const info = agentInfo.get(ch)!
    subEl.textContent = ` · ${info.name}`
    dotEl.style.background = info.color || ''
  } else {
    subEl.textContent = ''
    dotEl.style.background = ''
  }
}

export function renderTabs() {
  // Single agent: no tab bar — just auto-select it and update header
  if (agentInfo.size === 1) {
    const [id] = agentInfo.keys()
    if (activeChannel !== id) { setActiveChannel(id); checkAgentOnline(id) }
    updateHeader(id)
    tabsEl.classList.remove('visible')
    if (inputEl) inputEl.disabled = false
    return
  }

  // Zero agents: no tabs
  if (agentInfo.size === 0) {
    tabsEl.classList.remove('visible')
    if (inputEl) inputEl.disabled = false
    return
  }

  // Multiple agents: show ALL + per-agent tabs
  restoreActiveChannel()   // first multi-agent render: re-apply the saved tab
  tabsEl.classList.add('visible')
  tabsEl.innerHTML = ''

  const allTab = document.createElement('button')
  allTab.className = 'pa-tab' + (activeChannel === null ? ' active' : '')
  allTab.style.setProperty('--tab-color', 'var(--pa-green)')
  allTab.innerHTML = '<span class="pa-tab-pip"></span>All'
  allTab.addEventListener('click', () => switchChannel(null))
  tabsEl.appendChild(allTab)

  for (const [id, info] of agentInfo) {
    if (archived.has(id)) continue
    const tab = document.createElement('button')
    tab.className = 'pa-tab' + (activeChannel === id ? ' active' : '')
    tab.style.setProperty('--tab-color', info.color || 'var(--pa-green)')
    tab.title = id
    const count = unreadByChannel[id] || 0
    const idLabel = id !== info.name.toLowerCase().replace(/\s+/g, '-') ? `<span class="pa-tab-id">${esc(id)}</span>` : ''
    tab.innerHTML = `<span class="pa-tab-pip"></span><span class="pa-tab-label-wrap">${esc(info.name)}${idLabel}</span><span class="pa-tab-unread${count ? ' visible' : ''}" id="pa-tab-unread-${id}">${count || ''}</span><span class="pa-tab-x" title="Archive this tab">×</span>`
    tab.addEventListener('click', () => switchChannel(id))
    tab.querySelector('.pa-tab-x')!.addEventListener('click', (e) => {
      e.stopPropagation()
      archiveChannel(id)
    })
    tabsEl.appendChild(tab)
  }

  // Archived tabs collapse behind a dropdown at the end of the bar
  const archList = [...agentInfo.entries()].filter(([id]) => archived.has(id))
  if (archList.length > 0) {
    const wrap = document.createElement('div')
    wrap.className = 'pa-arch-wrap'
    const archUnread = archList.reduce((n, [id]) => n + (unreadByChannel[id] || 0), 0)
    const btn = document.createElement('button')
    btn.className = 'pa-tab pa-arch-btn'
    btn.innerHTML = `Archived (${archList.length}) ▾<span class="pa-tab-unread${archUnread ? ' visible' : ''}">${archUnread || ''}</span>`
    const menu = document.createElement('div')
    menu.className = 'pa-arch-menu'
    for (const [id, info] of archList) {
      const row = document.createElement('button')
      row.className = 'pa-arch-row'
      row.style.setProperty('--tab-color', info.color || 'var(--pa-green)')
      row.title = `Restore ${id}`
      const count = unreadByChannel[id] || 0
      row.innerHTML = `<span class="pa-tab-pip"></span><span>${esc(info.name)}</span><span class="pa-arch-row-id">${esc(id)}</span>${count ? `<span class="pa-tab-unread visible" style="position:static">${count}</span>` : ''}`
      row.addEventListener('click', () => {
        unarchiveChannel(id)   // opening an archived tab restores it
        switchChannel(id)
      })
      menu.appendChild(row)
    }
    btn.addEventListener('click', (e) => {
      e.stopPropagation()
      const opening = !menu.classList.contains('open')
      menu.classList.toggle('open')
      if (opening) {
        document.addEventListener('click', () => menu.classList.remove('open'), { once: true })
      }
    })
    wrap.appendChild(btn)
    wrap.appendChild(menu)
    tabsEl.appendChild(wrap)
  }

  // Disable input + show hint when ALL is active (no broadcast)
  if (inputEl) {
    const onAll = activeChannel === null
    inputEl.disabled = onAll
    inputEl.placeholder = onAll ? 'Select an agent tab to reply…' : 'Message Agent…'
  }

  updateHeader(activeChannel)
}

async function checkAgentOnline(ch: string) {
  try {
    const r = await fetch(`${CHAT_BASE}/subscribers`)
    if (!r.ok) return
    const data = await r.json() as { poll?: { channels?: { channel: string | null }[] } }
    const pollers = data.poll?.channels ?? []
    const online = pollers.some(p => p.channel === ch)
    if (!online && connBanner) {
      connBanner.className = 'pa-conn-banner reconnecting show'
      connBanner.textContent = `Agent not listening — run: parlay monitor --agent ${ch}`
    }
  } catch {}
}

export function switchChannel(ch: string | null) {
  setActiveChannel(ch)
  try { localStorage.setItem(ACTIVE_KEY, ch ?? '') } catch {}   // persist the choice
  if (ch !== null) unreadByChannel[ch] = 0
  renderTabs()
  if (_renderThread) _renderThread()
  if (ch !== null && connBanner) {
    connBanner.className = 'pa-conn-banner'
    connBanner.textContent = ''
    checkAgentOnline(ch)
  }
}
