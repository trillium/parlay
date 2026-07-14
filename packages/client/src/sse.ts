import { CHAT_BASE } from './config'
import { PA_VERSION } from './version'
import {
  msgs, open, unread,
  agentInfo, unreadByChannel,
  setUnread, setEs, setReconnectDelay, setReconnectTimer,
  reconnectDelay, setChannelStatuses,
} from './state'
import { connBanner, dot, drawer, badge, inputEl } from './dom'
import { appendMsg, loadHistory as loadHistoryFn, setThinking, insertLavishCard } from './thread'
import { draftClientId, lastSendTs } from './input'
import { navigateWorkspace } from './commands/ctx'
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

export function speak(text: string, msgId?: string) {
  if ((window as any).__paSpeak) (window as any).__paSpeak(text, msgId)
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

// ── Plugin SSE subscriptions ──────────────────────────────────────────────────
// Raw listeners on __paEs die when connect() replaces the EventSource on
// reconnect — plugins subscribe here and get re-attached automatically.
const pluginSse: Array<[string, (data: any) => void]> = []
function attachPluginHandlers(es: EventSource) {
  for (const [event, handler] of pluginSse) {
    es.addEventListener(event, (e: MessageEvent) => { try { handler(JSON.parse(e.data)) } catch {} })
  }
}
export function onSse(event: string, handler: (data: any) => void) {
  pluginSse.push([event, handler])
  const es = (window as any).__paEs as EventSource | null
  if (es) es.addEventListener(event, (e: MessageEvent) => { try { handler(JSON.parse(e.data)) } catch {} })
}

// ── SSE connection ────────────────────────────────────────────────────────────

export function connect() {
  const currentEs: EventSource | null = (window as any).__paEs ?? null
  if (currentEs) { try { currentEs.close() } catch {} }

  const es = new EventSource(`${CHAT_BASE}/events?device=${encodeURIComponent(getDeviceId())}`)
  attachPluginHandlers(es)
  ;(window as any).__paEs = es
  setEs(es)

  es.addEventListener('connected', () => {
    setReconnectDelay(1000)
    connBanner.className = 'pa-conn-banner'
    connBanner.textContent = ''
    dot.classList.remove('offline')
    drawer.classList.remove('agent-away')
    // Self-upgrade: PWA pages live for days — on every (re)connect compare our
    // compiled-in version to the served bundle and reload once if stale. The
    // sessionStorage guard prevents reload loops when a cache stays sticky.
    fetch(`${CHAT_BASE}/version`).then(r => r.json()).then(({ version }) => {
      if (!version || version === 'unknown' || version === PA_VERSION) return
      if (sessionStorage.getItem('pa-upgrade-attempt') === version) return
      sessionStorage.setItem('pa-upgrade-attempt', version)
      location.reload()
    }).catch(() => {})
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

    // Render BEFORE speaking: speak() highlights the rendered block spans, so
    // the message must be in the DOM first (auto-TTS highlight bug)
    if (open && inView) {
      appendMsg(m)
    } else if (!open && m.role === 'agent' && m.type !== 'system_update') {
      const newUnread = unread + 1
      setUnread(newUnread)
      badge.textContent = newUnread > 9 ? '9+' : String(newUnread)
      badge.classList.add('visible')
    }

    if (m.role === 'user') {
      armCompactTimer()
    } else if (m.role === 'agent') {
      if (m.type !== 'system_update') clearCompactTimer()
      if (inView && m.type !== 'action_request' && m.type !== 'system_update') speak(m.text, m.id)
      // Per-tab unread badge when this channel's tab is not active
      if (!inView && m.channel && m.type !== 'system_update') {
        unreadByChannel[m.channel] = (unreadByChannel[m.channel] || 0) + 1
        const tabBadge = document.getElementById(`pa-tab-unread-${m.channel}`)
        if (tabBadge) {
          tabBadge.textContent = String(unreadByChannel[m.channel])
          tabBadge.classList.add('visible')
        }
      }
    }
  })

  es.addEventListener('presence', (e: MessageEvent) => {
    const { status } = JSON.parse(e.data)
    setThinking(status === 'thinking')
  })

  es.addEventListener('draft', (e: MessageEvent) => {
    const { text, clientId } = JSON.parse(e.data)
    // Self-echoes refilled a just-sent input on mobile (bug #4): ignore our own
    // PUTs and anything in the 3s post-send window. Other devices' drafts
    // still sync — that's the feature's point.
    if (clientId && clientId === draftClientId) return
    if (Date.now() - lastSendTs < 3_000) return
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
    // Parlay-as-shell (#16): on chat-app the workspace iframe absorbs the
    // navigation and the chat survives; elsewhere this is a full navigation
    if (url) navigateWorkspace(url)
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
