/**
 * pulse-agent.js — Persistent agent drawer for all Pulse pages.
 * One <script src="/annotate/pulse-agent.js"></script> on any page.
 *
 * Transport: SSE from /api/chat/events, POST to /api/chat/send & /api/chat/reply.
 * Multi-agent: agents register via reply { agent, name, color } → tab per agent.
 * Annotation: click any element → queue comment → send batch as chat message.
 */

import { IS_STANDALONE, DESKTOP_BP } from './config'
import {
  open, setOpen, setUnread, setAtBottom,
  ttsEnabled, ttsVoice, setTtsEnabled, setTtsVoice,
} from './state'
import { injectDOM, bindDOMRefs, setBodyMargin } from './dom'
import * as domRefs from './dom'
import { setRenderThreadFn, msgInView } from './tabs'
import { initAgentSwitcher } from './switcher'
import { renderThread } from './thread'
import { wireToolLogEvents } from './toollog'
import { connect, setOpenDrawerFn } from './sse'
import { wireInputEvents, loadDraft, sendMsg } from './input'
import { wireAnnotation, doSetAnnotate } from './annotation'
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

thread.addEventListener('scroll', () => {
  setAtBottom(thread.scrollTop + thread.clientHeight >= thread.scrollHeight - 50)
  // Debounced save of scroll position as a ratio
  clearTimeout(scrollSaveTimer!)
  scrollSaveTimer = setTimeout(() => {
    const max = thread.scrollHeight - thread.clientHeight
    if (max > 0) {
      localStorage.setItem(SCROLL_KEY, String(thread.scrollTop / max))
    }
  }, 300)
})

// ── TTS ──────────────────────────────────────────────────────────────────────
const ttsBtn = document.getElementById('pa-tts-btn')!

function initTTSVoice() {
  if (!('speechSynthesis' in window)) return
  const voices = speechSynthesis.getVoices()
  setTtsVoice(
    voices.find(v => v.name === 'Samantha') ||
    voices.find(v => v.name === 'Karen') ||
    voices.find(v => v.lang === 'en-US') ||
    voices.find(v => v.lang.startsWith('en')) ||
    voices[0] || null
  )
}

function clearSpeakingHighlight() {
  document.querySelectorAll('.pa-speaking').forEach(el => el.classList.remove('pa-speaking'))
}

function speak(text: string, msgId?: string) {
  if (!ttsEnabled || !('speechSynthesis' in window)) return
  speechSynthesis.cancel()
  clearSpeakingHighlight()
  const utt = new SpeechSynthesisUtterance(text)
  if (ttsVoice) utt.voice = ttsVoice
  utt.rate = 1.05
  // Highlight the message being spoken for its whole playback
  const bubble = msgId
    ? document.querySelector(`[data-pa-id="${msgId}"] .pa-bubble`)
    : null
  if (bubble) {
    utt.onstart = () => bubble.classList.add('pa-speaking')
    utt.onend = utt.onerror = () => bubble.classList.remove('pa-speaking')
  }
  speechSynthesis.speak(utt)
}
;(window as any).__paSpeak = speak

// Hard-stop ALL speech output — voice command "spoken pause" routes here.
// Covers speechSynthesis now and the server-TTS <audio> element when present.
function stopSpeak() {
  try { if ('speechSynthesis' in window) speechSynthesis.cancel() } catch {}
  const au = document.getElementById('pa-tts-audio') as HTMLAudioElement | null
  if (au) { try { au.pause(); au.currentTime = 0 } catch {} }
  clearSpeakingHighlight()
}
;(window as any).__paStopSpeak = stopSpeak

if ('speechSynthesis' in window) {
  speechSynthesis.addEventListener('voiceschanged', initTTSVoice)
  initTTSVoice()
  ttsBtn.addEventListener('click', () => {
    setTtsEnabled(!ttsEnabled)
    ttsBtn.classList.toggle('active', ttsEnabled)
    if (!ttsEnabled) speechSynthesis.cancel()
  })
} else {
  (ttsBtn as HTMLElement).style.display = 'none'
}

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
wireToolLogEvents()
wireInputEvents()
if (isDesktop() || IS_STANDALONE) openDrawer(true)
connect()
loadDraft()

})() // end async IIFE
