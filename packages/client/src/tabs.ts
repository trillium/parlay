import { esc, fmtTime } from './config'
import { agentInfo, activeChannel, unreadByChannel, setActiveChannel, channelStatus, lastSeenByChannel, lastActivityByChannel, toolLogVisible, type AgentInfo } from './state'
import { checkAgentOnline } from './tab-online'
import { tabsEl, inputEl, connBanner, versionEl, urlsEl } from './dom'

// Nickname takes precedence over name for all human-facing display.
function displayName(info: AgentInfo): string { return info.nicknames?.[0] ?? info.name }
import { sheetOpen, renderSheet } from './switcher'
import { renderToolLog } from './toollog'
import { PA_VERSION } from './version'

// Left-edge build label doubles as the active-tab cue: "v3.7.5 · First Mate".
// The agent name is the load-bearing part (which tab am I in?), so it trails
// the version — if the rotated string ever clips at the top edge, the version
// is what goes, not the name. Falls back to the bare version with no channel.
function updateVersionLabel(ch: string | null) {
  if (!versionEl) return
  const info = ch ? agentInfo.get(ch) : null
  const label = info ? displayName(info) : null
  versionEl.textContent = label ? `v${PA_VERSION} · ${label}` : `v${PA_VERSION}`
}

// ── Per-channel status (green = listening, grey = idle, hollow = offline) ────
export function statusOf(id: string): 'listening' | 'idle' | 'offline' {
  return channelStatus[id] ?? 'offline'
}
function statusTooltip(id: string): string {
  const st = statusOf(id)
  if (st === 'listening') return `${id} — listening`
  const seen = lastSeenByChannel[id]
  if (st === 'idle') return `${id} — idle${seen ? `, last seen ${fmtTime(seen)}` : ''}`
  return `${id} — offline (never seen listening)`
}

let _renderThread: (() => void) | null = null
export function setRenderThreadFn(fn: () => void) { _renderThread = fn }

// Archived (stagnant) agent tabs — hidden behind a dropdown, persisted locally.
const ARCHIVED_KEY = 'pa-archived-channels'
export const archived = new Set<string>((() => {
  try { return JSON.parse(localStorage.getItem(ARCHIVED_KEY) || '[]') } catch { return [] }
})())
function persistArchived() {
  try { localStorage.setItem(ARCHIVED_KEY, JSON.stringify([...archived])) } catch {}
}
export function archiveChannel(id: string) {
  archived.add(id)
  persistArchived()
  if (activeChannel === id) {
    // Switch to the first remaining unarchived agent; if none, keep the
    // (now archived) channel active — chat still routes to it.
    const next = [...agentInfo.keys()].find(ch => !archived.has(ch))
    if (next) { switchChannel(next); return }
  }
  renderTabs()
}
export function unarchiveChannel(id: string) {
  archived.delete(id)
  persistArchived()
}

// Persist the selected tab across refreshes.
const ACTIVE_KEY = 'pa-active-channel'
let _restored = false
function restoreActiveChannel() {
  if (_restored) return
  _restored = true
  // URL ownership: agent whose registered urls match this page is the default — beats last-tab memory.
  const owner = [...agentInfo.entries()].find(([, i]) => i.urls?.some(u => window.location.href.startsWith(u)))?.[0]
  if (owner) { setActiveChannel(owner); return }
  let saved: string | null = null
  try { saved = localStorage.getItem(ACTIVE_KEY) } catch {}
  if (saved && agentInfo.has(saved)) setActiveChannel(saved)
}

export function msgInView(m: any): boolean {
  if (activeChannel === null) return true   // zero-agent state only — no All view exists
  // Channel-tagged messages (either role) are strictly scoped to their own tab.
  // A captain message sent in agent A's tab carries channel=A, so it no longer
  // leaks into agent B's tab (#21 cross-tab leak — user messages used to return
  // true unconditionally here).
  if (m.channel) return m.channel === activeChannel
  // Channel-less messages: a user-role one is a global relay/alert with no
  // target and shows in every tab; a channel-less agent line shows nowhere
  // (preserves the #13 no-leak behavior for legacy system lines).
  return m.role === 'user'
}

function updateHeader(ch: string | null) {
  updateVersionLabel(ch)
  const subEl = document.getElementById('pa-sub')
  const dotEl = document.getElementById('pa-dot')
  if (!subEl || !dotEl) return
  if (ch && agentInfo.has(ch)) {
    const info = agentInfo.get(ch)!
    subEl.textContent = ` · ${displayName(info)}`
    // Header dot carries the channel's live status, even in single-agent mode
    const st = statusOf(ch)
    dotEl.style.background = st === 'listening' ? (info.color || 'var(--pa-green)')
                           : st === 'idle'      ? 'var(--pa-muted)' : 'transparent'
    dotEl.style.boxShadow  = st === 'offline' ? 'inset 0 0 0 1.5px var(--pa-muted)' : ''
    dotEl.title = statusTooltip(ch)
    // URL pills — clickable links to pulse pages this agent owns
    if (urlsEl) {
      const urls = info.urls ?? []
      urlsEl.innerHTML = urls.map(u => `<a class="pa-url-pill" href="${esc(u)}" title="${esc(u)}" target="_blank">${esc(u.replace(/^https?:\/\/[^/]+/, '') || u)}</a>`).join('')
      urlsEl.classList.toggle('visible', urls.length > 0)
    }
  } else {
    subEl.textContent = ''
    dotEl.style.background = ''
    dotEl.style.boxShadow = ''
    if (urlsEl) { urlsEl.innerHTML = ''; urlsEl.classList.remove('visible') }
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

  // Multiple agents: one tab per agent (no All view — per-agent chats only)
  const prevActive = activeChannel
  restoreActiveChannel()   // first multi-agent render: re-apply the saved tab
  // An agent tab is always selected — default to the first unarchived agent
  if (!activeChannel || !agentInfo.has(activeChannel)) {
    const first = [...agentInfo.keys()].find(id => !archived.has(id) && id !== 'system')
      ?? [...agentInfo.keys()].find(id => !archived.has(id)) ?? [...agentInfo.keys()][0]
    setActiveChannel(first)
  }
  // History renders before the agents list arrives (activeChannel null →
  // everything passed msgInView) — re-filter the thread once a real channel
  // is selected, whether restored from localStorage or defaulted (#13)
  if (prevActive !== activeChannel && _renderThread) _renderThread()
  tabsEl.classList.add('visible')
  tabsEl.innerHTML = ''

  const makeTab = (id: string, info: AgentInfo) => {
    const tab = document.createElement('button')
    tab.className = 'pa-tab' + (activeChannel === id ? ' active' : '') + (id === 'system' ? ' pa-tab-system' : '')
    tab.style.setProperty('--tab-color', info.color || 'var(--pa-green)')
    const label = displayName(info)
    // Tooltip: show status + id when nickname differs from id, plus any owned URLs
    const urlHint = info.urls?.length ? `\n${info.urls.join('\n')}` : ''
    tab.title = id === 'system' ? 'Hook & tool events from all sessions' : statusTooltip(id) + urlHint
    const count = unreadByChannel[id] || 0
    const idLabel = id !== label.toLowerCase().replace(/\s+/g, '-') ? `<span class="pa-tab-id">${esc(id)}</span>` : ''
    tab.innerHTML = `<span class="pa-tab-pip ${statusOf(id)}"></span><span class="pa-tab-label-wrap">${esc(label)}${idLabel}</span><span class="pa-tab-unread${count ? ' visible' : ''}" id="pa-tab-unread-${id}">${count || ''}</span><span class="pa-tab-x" title="Archive this tab">×</span>`
    tab.addEventListener('click', () => switchChannel(id))
    tab.querySelector('.pa-tab-x')!.addEventListener('click', (e) => {
      e.stopPropagation()
      archiveChannel(id)
    })
    tabsEl.appendChild(tab)
  }

  // Sort: most-recently-active first, then listening agents, then alphabetical by name.
  // System pseudo-tab is excluded here and always rendered last (below).
  const sortedAgents = [...agentInfo.entries()]
    .filter(([id]) => !archived.has(id) && id !== 'system')
    .sort(([a, ai], [b, bi]) => {
      const aTime = lastActivityByChannel[a] ?? 0
      const bTime = lastActivityByChannel[b] ?? 0
      if (bTime !== aTime) return bTime - aTime
      const aListen = channelStatus[a] === 'listening' ? 1 : 0
      const bListen = channelStatus[b] === 'listening' ? 1 : 0
      if (bListen !== aListen) return bListen - aListen
      return displayName(ai).localeCompare(displayName(bi))
    })
  for (const [id, info] of sortedAgents) makeTab(id, info)
  // System pseudo-tab always renders LAST; archiving it (the ×) is the
  // per-device way to hide it — restore from the Archived dropdown.
  const sysInfo = agentInfo.get('system')
  if (sysInfo && !archived.has('system')) makeTab('system', sysInfo)

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
      row.innerHTML = `<span class="pa-tab-pip"></span><span>${esc(displayName(info))}</span><span class="pa-arch-row-id">${esc(id)}</span>${count ? `<span class="pa-tab-unread visible" style="position:static">${count}</span>` : ''}`
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

  // Input is always enabled and targets the selected agent's channel
  if (inputEl) {
    inputEl.disabled = false
    inputEl.placeholder = 'Message Agent…'
  }

  updateHeader(activeChannel)
  if (sheetOpen()) renderSheet()   // keep switcher sheet dots/unreads live
}

export function switchChannel(ch: string) {
  setActiveChannel(ch)
  try { localStorage.setItem(ACTIVE_KEY, ch) } catch {}   // persist the choice
  unreadByChannel[ch] = 0
  renderTabs()
  if (_renderThread) _renderThread()
  if (toolLogVisible) renderToolLog()   // keep the tool log scoped to the new tab
  if (connBanner && ch !== 'system') {   // System pseudo-tab has no poller to check
    connBanner.className = 'pa-conn-banner'
    connBanner.textContent = ''
    checkAgentOnline(ch)
  }
}
