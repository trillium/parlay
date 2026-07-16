/**
 * pulse-agent.js — Persistent agent drawer for all Pulse pages.
 * One <script src="/annotate/pulse-agent.js"></script> on any page.
 *
 * Transport: SSE from /api/chat/events, POST to /api/chat/send & /api/chat/reply.
 * Multi-agent: agents register via reply { agent, name, color } → tab per agent.
 * Annotation: click any element → queue comment → send batch as chat message.
 */

import { IS_STANDALONE, DESKTOP_BP } from './config'
import { toggleDebugPanel } from './debug-panel'
import { open, setOpen, setUnread, setAtBottom, activeChannel, unreadByChannel, agentInfo } from './state'
import { initCommands } from './commands'
import { initPlugins } from './plugins'
import { initLightbox } from './lightbox'
import { injectDOM, bindDOMRefs, setBodyMargin } from './dom'
import * as domRefs from './dom'
import { setRenderThreadFn, msgInView } from './tabs'
import { initAgentSwitcher } from './switcher'
import { renderThread } from './thread'
import { scrollBottom } from './thread-scroll'
import { wireToolLogEvents } from './toollog'
import { connect, setOpenDrawerFn, onSse } from './sse'
import { wireInputEvents, loadDraft, sendMsg, wireServerEval } from './input'
import { wireAnnotation, doSetAnnotate } from './annotation'
import { trackFocusTitle } from './focus-title'
import { injectPageNav, openPageNav } from './page-nav'
import {
  loadSettings, applySettings, isPageEnabled,
  injectSettingsModal, openSettingsModal,
  getSettings,
} from './settings-modal'

// Idempotency guard — only one instance per page
;(async () => {
if (document.getElementById('pa-drawer')) return

// ── Load settings before rendering ─────────────────────────────────────────
const settings = await loadSettings()

// Project filter — bail if this page is not enabled in settings
if (!isPageEnabled(settings)) return

// ── Inject HTML + CSS ───────────────────────────────────────────────────────
injectDOM()
bindDOMRefs()

const { backdrop, trigger, badge, drawer, thread, connBanner, inputEl } = domRefs
const { annToggle, annStrip, annCount, annList, annSend } = domRefs
const { popup, popupLbl, popupIn, popupOk, popupCx, settingsGearBtn } = domRefs

setRenderThreadFn(renderThread)
;(window as any).__paMsgInView = msgInView
initAgentSwitcher()
// Reserved System pseudo-channel: hook/tool system_update lines land here
// instead of leaking into every agent tab (scoping #13)
agentInfo.set('system', { id: 'system', name: 'System', color: '#6b7280' })

// ── Desktop detection ───────────────────────────────────────────────────────
function isDesktop() { return window.innerWidth >= DESKTOP_BP }

// ── Open / close ─────────────────────────────────────────────────────────────
function openDrawer(skipFocus?: boolean) {
  setOpen(true)
  drawer.classList.add('open')
  if (!isDesktop()) backdrop.classList.add('open')
  trigger.classList.add('open')
  setUnread(0)
  badge.classList.remove('visible')
  setBodyMargin(isDesktop(), getSettings().panelSide)
  if (!skipFocus) setTimeout(() => inputEl.focus(), 240)
}

function closeDrawer() {
  if (isDesktop()) return
  setOpen(false)
  drawer.classList.remove('open')
  backdrop.classList.remove('open')
  trigger.classList.remove('open')
  setBodyMargin(false, getSettings().panelSide)
  doSetAnnotate(false)
}

setOpenDrawerFn(openDrawer)
;(window as any).__paOpenDrawer = openDrawer

// Window-title focus marker: while the composer input is focused, the host
// page title carries `[focus:parlay-input]` for external watchers (Talon).
trackFocusTitle(inputEl, 'parlay-input')

function syncLayout() {
  if (isDesktop()) {
    if (!open) openDrawer(true)
    setBodyMargin(true, getSettings().panelSide)
  } else {
    if (open) setBodyMargin(false, getSettings().panelSide)
  }
}
window.addEventListener('resize', syncLayout, { passive: true })
trigger.addEventListener('click', () => open ? closeDrawer() : openDrawer())
backdrop.addEventListener('click', closeDrawer)
document.getElementById('pa-close')!.addEventListener('click', closeDrawer)
const SCROLL_KEY = 'pa-scroll-pct'
let scrollSaveTimer: ReturnType<typeof setTimeout> | null = null

const jumpBtn = document.getElementById('pa-jump')!
jumpBtn.addEventListener('click', () => {
  scrollBottom(true, true)   // instant — no smooth animation on a long catch-up
  setUnread(0)
  badge.classList.remove('visible')
  const ch = activeChannel
  if (ch) {
    unreadByChannel[ch] = 0
    const tabBadge = document.getElementById(`pa-tab-unread-${ch}`)
    if (tabBadge) { tabBadge.textContent = ''; tabBadge.classList.remove('visible') }
  }
})

thread.addEventListener('scroll', () => {
  const atBottomNow = thread.scrollTop + thread.clientHeight >= thread.scrollHeight - 50
  setAtBottom(atBottomNow)
  jumpBtn.classList.toggle('visible', !atBottomNow)
  // Debounced save of scroll position as a ratio
  clearTimeout(scrollSaveTimer!)
  scrollSaveTimer = setTimeout(() => {
    const max = thread.scrollHeight - thread.clientHeight
    if (max > 0) {
      localStorage.setItem(SCROLL_KEY, String(thread.scrollTop / max))
    }
  }, 300)
})

// ── TTS is the `speak` plugin now (#19) — loaded via initPlugins() below ────

// ── Compaction detection ──────────────────────────────────────────────────────
let compactTimer: ReturnType<typeof setTimeout> | null = null
function armCompactTimer() {
  clearTimeout(compactTimer!)
  compactTimer = setTimeout(() => {
    const dot = document.getElementById('pa-dot')!
    const sub = document.getElementById('pa-sub')!
    dot.className = 'pa-dot thinking'
    sub.textContent = ' · compacting…'
    drawer.classList.add('compacting')
    connBanner.className = 'pa-conn-banner reconnecting show'
    connBanner.textContent = 'agent compacting — will resume shortly'
    if (!open) openDrawer()
  }, 45_000)
}
function clearCompactTimer() {
  clearTimeout(compactTimer!)
  compactTimer = null
  drawer.classList.remove('compacting')
  connBanner.className = 'pa-conn-banner'
  connBanner.textContent = ''
}
;(window as any).__paArmCompact = armCompactTimer
;(window as any).__paClearCompact = clearCompactTimer

// ── Settings modal ────────────────────────────────────────────────────────────
injectSettingsModal()
settingsGearBtn.addEventListener('click', openSettingsModal)
applySettings(settings)

// ── Page-nav picker ───────────────────────────────────────────────────────────
injectPageNav()
document.getElementById('pa-nav-btn')?.addEventListener('click', openPageNav)

// ── Debug panel (Ctrl+Shift+D) ────────────────────────────────────────────────
document.addEventListener('keydown', (e) => { if (e.ctrlKey && e.shiftKey && e.key === 'D') { e.preventDefault(); toggleDebugPanel() } })

// ── Annotation ────────────────────────────────────────────────────────────────
wireAnnotation(
  annToggle, annStrip, annCount, annList, annSend,
  popup, popupLbl, popupIn, popupOk, popupCx,
  openDrawer, sendMsg,
)

// ── Scroll position restore ──────────────────────────────────────────────────
;(window as any).__paRestoreScroll = () => {
  const raw = localStorage.getItem(SCROLL_KEY)
  const pct = raw === null ? 1 : parseFloat(raw)
  const max = thread.scrollHeight - thread.clientHeight
  if (max <= 0) return
  if (!isFinite(pct) || pct >= 0.97) {
    // was at (near) bottom — settle exactly at the bottom after layout, instantly
    thread.scrollTo({ top: max, behavior: 'instant' as ScrollBehavior })
  } else {
    thread.scrollTo({ top: pct * max, behavior: 'instant' as ScrollBehavior })
  }
}

// ── Wire remaining events + connect ──────────────────────────────────────────
initCommands()   // voice/text command subsystem (src/commands/, COMMANDS.md)
initPlugins()    // plugin loader — before connect() so SSE subscriptions catch the first burst
initLightbox()   // shared image lightbox — delegated over all .pa-img surfaces
wireToolLogEvents()
wireInputEvents()
// Server-side eval: subscribe the action dispatcher to the input_action SSE
// channel. onSse auto-reattaches across reconnects, so the action channel
// survives connection drops with zero extra code.
wireServerEval(onSse)
if (isDesktop() || IS_STANDALONE) openDrawer(true)
connect()
loadDraft()

})() // end async IIFE
