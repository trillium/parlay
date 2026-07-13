import { CHAT_BASE } from './config'
import {
  msgs, open, unread,
  agentInfo, unreadByChannel,
  setUnread, setEs, setReconnectDelay, setReconnectTimer,
  reconnectDelay, setChannelStatuses,
} from './state'
import { connBanner, dot, drawer, badge, inputEl } from './dom'
import { appendMsg, loadHistory as loadHistoryFn, setThinking, insertLavishCard } from './thread'
import { renderTabs } from './tabs'
import { appendToolEntry } from './toollog'

// ── Compact timer ─────────────────────────────────────────────────────────────

let compactTimer: ReturnType<typeof setTimeout> | null = null

export function armCompactTimer() {
  clearTimeout(compactTimer!)
  compactTimer = setTimeout(() => {
    dot.className = 'pa-dot thinking'
    const sub = document.getElementById('pa-sub')!
    sub.textContent = ' · compacting…'
    drawer.classList.add('compacting')
    connBanner.className = 'pa-conn-banner reconnecting show'
    connBanner.textContent = 'agent compacting — will resume shortly'
    if (!open) openDrawerFn()
  }, 45_000)
}

export function clearCompactTimer() {
  clearTimeout(compactTimer!)
  compactTimer = null
  drawer.classList.remove('compacting')
  connBanner.className = 'pa-conn-banner'
  connBanner.textContent = ''
}

// ── Speak callback (avoids circular dep) ──────────────────────────────────────

export function speak(text: string) {
  if ((window as any).__paSpeak) (window as any).__paSpeak(text)
}

// openDrawer is set by init.ts to break circular dep
let openDrawerFn: (skipFocus?: boolean) => void = () => {}
export function setOpenDrawerFn(fn: (skipFocus?: boolean) => void) { openDrawerFn = fn }

// ── Device identity ───────────────────────────────────────────────────────────
// Stable per-browser uuid so the server can scope navigate/reload to one device.

export function getDeviceId(): string {
  try {
    let id = localStorage.getItem('pa-device-id')
    if (!id) {
      id = crypto.randomUUID ? crypto.randomUUID() : `dev-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
      localStorage.setItem('pa-device-id', id)
    }
    return id
  } catch { return 'unknown' }
}
;(window as any).__paDeviceId = getDeviceId()

// ── SSE connection ────────────────────────────────────────────────────────────

export function connect() {
  const currentEs: EventSource | null = (window as any).__paEs ?? null
  if (currentEs) { try { currentEs.close() } catch {} }

  const es = new EventSource(`${CHAT_BASE}/events?device=${encodeURIComponent(getDeviceId())}`)
  ;(window as any).__paEs = es
  setEs(es)

  es.addEventListener('connected', () => {
    setReconnectDelay(1000)
    connBanner.className = 'pa-conn-banner'
    connBanner.textContent = ''
    dot.classList.remove('offline')
    drawer.classList.remove('agent-away')
  })

  es.addEventListener('history', (e: MessageEvent) => {
    const history = JSON.parse(e.data)
    loadHistoryFn(history)
    // After DOM settles, restore saved scroll position if user wasn't at bottom
    if ((window as any).__paRestoreScroll) {
      requestAnimationFrame(() => (window as any).__paRestoreScroll())
    }
  })

  es.addEventListener('agents', (e: MessageEvent) => {
    const list = JSON.parse(e.data)
    for (const info of list) agentInfo.set(info.id, info)
    renderTabs()
  })

  es.addEventListener('agent_register', (e: MessageEvent) => {
    const info = JSON.parse(e.data)
    agentInfo.set(info.id, info)
    renderTabs()
  })

  es.addEventListener('presence_map', (e: MessageEvent) => {
    setChannelStatuses(JSON.parse(e.data))
    renderTabs()
  })

  es.addEventListener('message', (e: MessageEvent) => {
    const m = JSON.parse(e.data)
    if (document.querySelector(`[data-pa-id="${m.id}"]`)) return
    msgs.push(m)
    ;(window as any).__paLastId = m.id

    const inView = (window as any).__paMsgInView ? (window as any).__paMsgInView(m) : true

    if (m.role === 'user') {
      armCompactTimer()
    } else if (m.role === 'agent') {
      clearCompactTimer()
      if (inView && m.type !== 'action_request') speak(m.text)
      // Per-tab unread badge when this channel's tab is not active
      if (!inView && m.channel) {
        unreadByChannel[m.channel] = (unreadByChannel[m.channel] || 0) + 1
        const tabBadge = document.getElementById(`pa-tab-unread-${m.channel}`)
        if (tabBadge) {
          tabBadge.textContent = String(unreadByChannel[m.channel])
          tabBadge.classList.add('visible')
        }
      }
    }

    if (open && inView) {
      appendMsg(m)
    } else if (!open && m.role === 'agent') {
      const newUnread = unread + 1
      setUnread(newUnread)
      badge.textContent = newUnread > 9 ? '9+' : String(newUnread)
      badge.classList.add('visible')
    }
  })

  es.addEventListener('presence', (e: MessageEvent) => {
    const { status } = JSON.parse(e.data)
    setThinking(status === 'thinking')
  })

  es.addEventListener('draft', (e: MessageEvent) => {
    const { text } = JSON.parse(e.data)
    if (document.activeElement !== inputEl) {
      inputEl.value = text
      if ((window as any).__paAutoResize) (window as any).__paAutoResize()
    }
  })

  es.addEventListener('agent_presence', (e: MessageEvent) => {
    const { active } = JSON.parse(e.data)
    drawer.classList.toggle('agent-away', !active)
  })

  es.addEventListener('tool_event', (e: MessageEvent) => {
    const ev = JSON.parse(e.data)
    appendToolEntry(ev)
    // Tool activity proves the agent is working, not compacting — clear any
    // false "compacting" banner and push the deadline out another window.
    // Only while a reply is pending (timer armed); never arm from scratch here.
    if (compactTimer) { clearCompactTimer(); armCompactTimer() }
  })

  es.addEventListener('lavish_session', (e: MessageEvent) => {
    const { key, file, proxyUrl, status } = JSON.parse(e.data)
    insertLavishCard(key, file, proxyUrl, status)
    if (status === 'open' && !open) openDrawerFn()
  })

  es.addEventListener('reload', () => location.reload())

  es.addEventListener('navigate', (e: MessageEvent) => {
    const { url, openDrawer: od } = JSON.parse(e.data)
    if (od && !open) openDrawerFn()
    if (url) location.href = url
  })

  es.onerror = () => {
    dot.classList.add('offline')
    connBanner.className = 'pa-conn-banner reconnecting show'
    connBanner.textContent = 'reconnecting…'
    es.close()
    const delay = reconnectDelay
    setReconnectTimer(setTimeout(() => connect(), delay))
    setReconnectDelay(Math.min(delay * 2, 30_000))
  }
}
