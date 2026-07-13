import { esc, linkify, fmtTime } from './config'
import { msgs, agentInfo, activeChannel, atBottom, thinking, setThinkingState } from './state'
import { thread, emptyEl } from './dom'
import { msgInView, switchChannel } from './tabs'
import { annotateMessage } from './annotation'
import { navigateWorkspace } from './commands/ctx'

// ── Scroll helper ─────────────────────────────────────────────────────────────

export function scrollBottom(force?: boolean, instant?: boolean) {
  if (force || atBottom) {
    // instant bypasses the thread's CSS scroll-behavior:smooth — used for initial
    // history render and tab switches, where an animated scroll is jarring
    if (instant) thread.scrollTo({ top: thread.scrollHeight, behavior: 'instant' as ScrollBehavior })
    else thread.scrollTop = thread.scrollHeight
  }
}

// ── Think indicator ───────────────────────────────────────────────────────────

export function addThinkEl() {
  if (document.getElementById('pa-think')) return
  const d = document.createElement('div')
  d.id = 'pa-think'
  d.className = 'pa-thinking'
  // The think indicator IS the responding agent: same initials + accent
  // treatment as _appendMsgEl. Generic AG only in the zero-agent state.
  const info = activeChannel ? agentInfo.get(activeChannel) : null
  const init  = info ? info.name.slice(0, 2).toUpperCase() : 'AG'
  const color = info ? info.color : 'var(--pa-green)'
  d.innerHTML = `<div class="pa-av agent" style="background:color-mix(in srgb,${color} 14%,var(--pa-ink));color:${color};border-color:color-mix(in srgb,${color} 22%,transparent)">${init}</div><div class="pa-thinking-dots"><b></b><b></b><b></b></div>`
  thread.appendChild(d)
  scrollBottom(true)
}

export function rmThinkEl() {
  document.getElementById('pa-think')?.remove()
}

export function setThinking(on: boolean) {
  setThinkingState(on)
  const dot = document.getElementById('pa-dot')!
  const sub = document.getElementById('pa-sub')!
  const name = activeChannel ? agentInfo.get(activeChannel)?.name : undefined
  dot.className = 'pa-dot' + (on ? ' thinking' : '')
  sub.textContent = on ? ' · thinking…' : (name ? ` · ${name}` : '')
  if (on) addThinkEl(); else rmThinkEl()
}

// ── Message rendering ─────────────────────────────────────────────────────────

// Internal: creates and appends a message element; does NOT manage emptyEl
export function _appendMsgEl(m: any) {
  rmThinkEl()
  const cls = m.role === 'agent' ? 'agent' : 'user'
  const info = (cls === 'agent' && m.channel) ? agentInfo.get(m.channel) : null
  const agentName  = info ? esc(info.name) : 'Agent'
  const agentColor = info ? info.color : 'var(--pa-green)'
  const agentInit  = info ? info.name.slice(0, 2).toUpperCase() : 'AG'

  const el = document.createElement('div')
  el.dataset.paId = m.id
  el.className = `pa-msg ${cls}`

  // System update (hook firings etc.) → thin muted line, no avatar, no bubble
  if (m.type === 'system_update') {
    el.className = 'pa-sysline'
    el.innerHTML = `<span class="pa-sysline-src">${esc(m.source ?? 'system')}</span><span class="pa-sysline-text">${linkify(esc(m.text))}</span><span class="pa-sysline-ts">${fmtTime(m.ts)}</span>`
    thread.appendChild(el)
    scrollBottom()
    return
  }

  // Agent-suggested action → inline card with a button. Nothing happens until
  // the captain clicks; the effect is local to this device only.
  if (cls === 'agent' && m.type === 'action_request' && m.action) {
    const a = m.action
    const btnLabel = a.kind === 'switch_tab' ? `Switch to ${esc(a.channel ?? '')}` : 'Go →'
    el.innerHTML = `
      <div class="pa-av agent" style="background:color-mix(in srgb,${agentColor} 14%,var(--pa-ink));color:${agentColor};border-color:color-mix(in srgb,${agentColor} 22%,transparent)">${agentInit}</div>
      <div class="pa-bc">
        <div class="pa-meta"><span class="pa-meta-n" style="color:${agentColor}">${agentName}</span><span>${fmtTime(m.ts)}</span></div>
        <div class="pa-action-card" style="border-color:color-mix(in srgb,${agentColor} 30%,var(--pa-border))">
          <span class="pa-action-icon">⇢</span>
          <span class="pa-action-label">${esc(a.label)}</span>
          <button class="pa-action-btn">${btnLabel}</button>
        </div>
      </div>`
    el.querySelector('.pa-action-btn')!.addEventListener('click', () => {
      if (a.kind === 'switch_tab' && a.channel) switchChannel(a.channel)
      else if (a.kind === 'navigate' && a.url && /^(https?:|\/)/i.test(a.url)) navigateWorkspace(a.url)
    })
    thread.appendChild(el)
    if (thinking) addThinkEl()
    scrollBottom()   // respects atBottom — arrival never yanks a scrolled-up thread
    return
  }

  if (cls === 'agent') {
    const channelId = m.channel ? `<span class="pa-meta-id">${esc(m.channel)}</span>` : ''
    el.innerHTML = `
      <div class="pa-av-col">
        <div class="pa-av agent" style="background:color-mix(in srgb,${agentColor} 14%,var(--pa-ink));color:${agentColor};border-color:color-mix(in srgb,${agentColor} 22%,transparent)">${agentInit}</div>
        <button class="pa-msg-ann" title="Comment on this reply">✎</button>
      </div>
      <div class="pa-bc">
        <div class="pa-meta"><span class="pa-meta-n" style="color:${agentColor}">${agentName}</span>${channelId}<span>${fmtTime(m.ts)}</span></div>
        <div class="pa-bubble agent" style="background:color-mix(in srgb,${agentColor} 7%,var(--pa-surf2));border-color:color-mix(in srgb,${agentColor} 16%,var(--pa-border));border-radius:3px 10px 10px 10px;font-family:var(--pa-mono);font-size:11.5px">${linkify(esc(m.text))}</div>
      </div>`
    el.style.cursor = 'pointer'
    el.title = 'Click to re-read aloud'
    el.addEventListener('click', () => {
      // speak() is wired in init.ts via a registered callback to avoid circular dep
      if ((window as any).__paSpeak) (window as any).__paSpeak(m.text, m.id)
    })
    // ✎ under the avatar: comment on THIS reply via the annotation popup —
    // works from anywhere in the scrollback, no annotate mode needed
    el.querySelector('.pa-msg-ann')!.addEventListener('click', (e) => {
      e.stopPropagation()   // don't trigger click-to-speak
      const bubble = el.querySelector('.pa-bubble') as HTMLElement
      const ev = e as MouseEvent
      annotateMessage(bubble, ev.clientX, ev.clientY)
    })
  } else {
    el.innerHTML = `
      <div class="pa-av user">YOU</div>
      <div class="pa-bc">
        <div class="pa-meta"><span class="pa-meta-n">You</span><span>${fmtTime(m.ts)}</span></div>
        <div class="pa-bubble user">${linkify(esc(m.text))}</div>
      </div>`
  }
  thread.appendChild(el)
  // Long captain messages clamp by default — tap the bubble to expand/collapse.
  // Measured synchronously post-append: rAF never fires in background tabs.
  if (cls === 'user') {
    const bubble = el.querySelector('.pa-bubble.user') as HTMLElement | null
    if (bubble && bubble.scrollHeight > 96) {
      bubble.classList.add('pa-expandable', 'pa-clamped')
      bubble.addEventListener('click', (e) => {
        if ((e.target as HTMLElement).tagName === 'A') return   // links still work
        bubble.classList.toggle('pa-clamped')
      })
    }
  }
  if (thinking) addThinkEl()
  scrollBottom()
}

// Public: called for new arriving messages; manages emptyEl + tab unread
export function appendMsg(m: any) {
  emptyEl.style.display = 'none'
  _appendMsgEl(m)
}

export function renderThread() {
  // Remove only message rows, keep emptyEl and think indicator
  Array.from(thread.children).forEach((ch: any) => {
    if (ch.id !== 'pa-empty' && ch.id !== 'pa-think') ch.remove()
  })
  const visible = msgs.filter(msgInView)
  if (visible.length === 0) {
    emptyEl.style.display = ''
  } else {
    emptyEl.style.display = 'none'
    visible.forEach((m: any) => _appendMsgEl(m))
  }
  scrollBottom(true, true)
}

// ── Lavish session card ───────────────────────────────────────────────────────

export function insertLavishCard(key: string, file: string, proxyUrl: string, status: string) {
  const name = file.split('/').pop() ?? file
  if (status === 'ended') {
    const el = document.getElementById(`pa-lavish-${key}`)
    if (el) el.classList.add('closed')
    return
  }
  emptyEl.style.display = 'none'
  const el = document.createElement('div')
  el.id = `pa-lavish-${key}`
  el.className = 'pa-lavish-card'
  el.innerHTML = `
    <div class="pa-lavish-icon">📄</div>
    <div class="pa-lavish-body">
      <div class="pa-lavish-label">Lavish artifact</div>
      <div class="pa-lavish-name">${esc(name)}</div>
    </div>
    <a class="pa-lavish-btn" href="${esc(proxyUrl)}" target="_blank">Open →</a>`
  thread.appendChild(el)
  scrollBottom(true)
}

export function loadHistory(history: any[]) {
  history.forEach((m: any) => {
    if (document.querySelector(`[data-pa-id="${m.id}"]`)) return
    msgs.push(m)
    // setLastId is in state but imported there; direct mutation here is fine for lastId
    ;(window as any).__paLastId = m.id
    if (msgInView(m)) appendMsg(m)
  })
  // Land at the bottom immediately — no animated catch-up scroll on page load
  scrollBottom(true, true)
}
