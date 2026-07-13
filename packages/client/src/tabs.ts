import { esc } from './config'
import { agentInfo, activeChannel, unreadByChannel, setActiveChannel } from './state'
import { tabsEl } from './dom'

// Imported lazily to break circular deps
let _renderThread: (() => void) | null = null
export function setRenderThreadFn(fn: () => void) { _renderThread = fn }

export function msgInView(m: any): boolean {
  if (activeChannel === null) return true
  // User messages appear in every tab
  if (m.role === 'user') return true
  // Agent messages: show if no channel (untagged) or matching channel
  return !m.channel || m.channel === activeChannel
}

export function renderTabs() {
  if (agentInfo.size === 0) {
    tabsEl.classList.remove('visible')
    return
  }
  tabsEl.classList.add('visible')
  tabsEl.innerHTML = ''

  // ALL tab
  const allTab = document.createElement('button')
  allTab.className = 'pa-tab' + (activeChannel === null ? ' active' : '')
  allTab.style.setProperty('--tab-color', 'var(--pa-green)')
  allTab.innerHTML = '<span class="pa-tab-pip"></span>All'
  allTab.addEventListener('click', () => switchChannel(null))
  tabsEl.appendChild(allTab)

  // Per-agent tabs
  for (const [id, info] of agentInfo) {
    const tab = document.createElement('button')
    tab.className = 'pa-tab' + (activeChannel === id ? ' active' : '')
    tab.style.setProperty('--tab-color', info.color || 'var(--pa-green)')
    tab.title = id  // tooltip shows slug on hover
    const count = unreadByChannel[id] || 0
    // Show name + dim agent ID slug below it for disambiguation
    const idLabel = id !== info.name.toLowerCase().replace(/\s+/g, '-') ? `<span class="pa-tab-id">${esc(id)}</span>` : ''
    tab.innerHTML = `<span class="pa-tab-pip"></span><span class="pa-tab-label-wrap">${esc(info.name)}${idLabel}</span><span class="pa-tab-unread${count ? ' visible' : ''}" id="pa-tab-unread-${id}">${count || ''}</span>`
    tab.addEventListener('click', () => switchChannel(id))
    tabsEl.appendChild(tab)
  }
}

export function switchChannel(ch: string | null) {
  setActiveChannel(ch)
  if (ch !== null) {
    unreadByChannel[ch] = 0
  }
  renderTabs()
  if (_renderThread) _renderThread()
}
