import { esc } from './config'
import { agentInfo, activeChannel, unreadByChannel, setActiveChannel } from './state'
import { tabsEl, inputEl } from './dom'

let _renderThread: (() => void) | null = null
export function setRenderThreadFn(fn: () => void) { _renderThread = fn }

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
    if (activeChannel !== id) setActiveChannel(id)
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
  tabsEl.classList.add('visible')
  tabsEl.innerHTML = ''

  const allTab = document.createElement('button')
  allTab.className = 'pa-tab' + (activeChannel === null ? ' active' : '')
  allTab.style.setProperty('--tab-color', 'var(--pa-green)')
  allTab.innerHTML = '<span class="pa-tab-pip"></span>All'
  allTab.addEventListener('click', () => switchChannel(null))
  tabsEl.appendChild(allTab)

  for (const [id, info] of agentInfo) {
    const tab = document.createElement('button')
    tab.className = 'pa-tab' + (activeChannel === id ? ' active' : '')
    tab.style.setProperty('--tab-color', info.color || 'var(--pa-green)')
    tab.title = id
    const count = unreadByChannel[id] || 0
    const idLabel = id !== info.name.toLowerCase().replace(/\s+/g, '-') ? `<span class="pa-tab-id">${esc(id)}</span>` : ''
    tab.innerHTML = `<span class="pa-tab-pip"></span><span class="pa-tab-label-wrap">${esc(info.name)}${idLabel}</span><span class="pa-tab-unread${count ? ' visible' : ''}" id="pa-tab-unread-${id}">${count || ''}</span>`
    tab.addEventListener('click', () => switchChannel(id))
    tabsEl.appendChild(tab)
  }

  // Disable input + show hint when ALL is active (no broadcast)
  if (inputEl) {
    const onAll = activeChannel === null
    inputEl.disabled = onAll
    inputEl.placeholder = onAll ? 'Select an agent tab to reply…' : 'Message Agent…'
  }

  updateHeader(activeChannel)
}

export function switchChannel(ch: string | null) {
  setActiveChannel(ch)
  if (ch !== null) unreadByChannel[ch] = 0
  renderTabs()
  if (_renderThread) _renderThread()
}
