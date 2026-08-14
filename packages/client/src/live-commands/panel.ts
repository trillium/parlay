// The live-command view: DOM rendering, the read-endpoint fetch, and the
// wiring that connects both to the panel. model.ts owns the data and the
// display rules; nothing here decides what a record means.
//
// Two properties this file is built around:
//
//  1. It must never break the panel. A server without the registry (older
//     than this feature) sends no `commands` burst and 404s the read
//     endpoint; that is rendered as "this server does not have it", never as
//     an error, and nothing else in the panel is affected.
//
//  2. It must be honest about what it cannot see. Only the Go CLI reports
//     itself, so an empty list means "nothing reported", not "nothing is
//     running". The view says so — see docs/live-commands.md.

import { CHAT_BASE, esc } from '../config'
import {
  liveCommands, liveCommandsVisible, liveCommandsSupported,
  setLiveCommandsVisible, setLiveCommandsSupported,
} from '../state'
import { cmdLog, cmdBtn } from '../dom'
import {
  type CommandInvocation, type CommandsResponse,
  COVERAGE_NOTE, UNSUPPORTED_NOTE,
  ingestSnapshot, ingestUpdate, clearCommandState,
  isRunning, sortCommands, liveDurationMs, commandAge, commandDetail,
} from './model'

// ── Ingest + repaint ──────────────────────────────────────────────────────────
// The public ingest entry points: model.ts mutates, this repaints if the view
// happens to be open. Keeping the repaint here is what lets model.ts stay
// DOM-free.

export function applyCommandsSnapshot(list: CommandInvocation[], now = Date.now()) {
  ingestSnapshot(list, now)
  if (liveCommandsVisible) renderLiveCommands()
}

export function applyCommandUpdate(rec: CommandInvocation, now = Date.now()) {
  ingestUpdate(rec, now)
  if (liveCommandsVisible) renderLiveCommands()
}

// ── Rendering ─────────────────────────────────────────────────────────────────

export function commandRowEl(rec: CommandInvocation, now = Date.now()): HTMLElement {
  const el = document.createElement('div')
  el.className = `pa-cmd-row ${isRunning(rec) ? 'running' : 'done'}`
  el.dataset.paCmd = rec.id
  const who = rec.agent || rec.channel || ''
  const detail = commandDetail(rec)
  el.innerHTML = `
    <span class="pa-cmd-dot ${esc(rec.state)}"></span>
    <span class="pa-cmd-verb">${esc(rec.verb)}</span>
    <span class="pa-cmd-who">${esc(who)}</span>
    <span class="pa-cmd-detail">${esc(detail)}</span>
    <span class="pa-cmd-age">${esc(commandAge(liveDurationMs(rec, now)))}</span>`
  return el
}

/**
 * Fill `host` with the current registry. Returns nothing — the assertions in
 * live-commands.test.ts read the produced DOM, the same DOM the panel shows.
 */
export function renderCommandsInto(host: HTMLElement, now = Date.now()) {
  host.innerHTML = ''
  const all = sortCommands([...liveCommands.values()])
  const running = all.filter(isRunning).length

  const head = document.createElement('div')
  head.className = 'pa-cmd-head'
  head.innerHTML = liveCommandsSupported === false
    ? '<span class="pa-cmd-title">LIVE COMMANDS</span><span class="pa-cmd-count">unavailable</span>'
    : `<span class="pa-cmd-title">LIVE COMMANDS</span><span class="pa-cmd-count">${running} running (${all.length} tracked)</span>`
  host.appendChild(head)

  if (liveCommandsSupported === false) {
    const note = document.createElement('div')
    note.className = 'pa-cmd-note'
    note.textContent = UNSUPPORTED_NOTE
    host.appendChild(note)
    return
  }

  for (const rec of all) host.appendChild(commandRowEl(rec, now))

  const note = document.createElement('div')
  note.className = 'pa-cmd-note'
  note.textContent = all.length
    ? COVERAGE_NOTE
    : `No commands are reporting. ${COVERAGE_NOTE}`
  host.appendChild(note)
}

export function renderLiveCommands(now = Date.now()) {
  if (!cmdLog) return
  renderCommandsInto(cmdLog, now)
}

// ── Fetch (the read endpoint, for open-before-any-SSE-event) ──────────────────

/**
 * Pull the registry over HTTP. Used when the view opens, so a panel that
 * connected before the server had anything to say still shows current state.
 * A 404 means an older server: recorded as unsupported, never as an error.
 */
export async function refreshLiveCommands(): Promise<void> {
  try {
    const res = await fetch(`${CHAT_BASE}/commands`)
    if (res.status === 404) {
      setLiveCommandsSupported(false)
      if (liveCommandsVisible) renderLiveCommands()
      return
    }
    if (!res.ok) return
    const body = await res.json() as CommandsResponse
    applyCommandsSnapshot(body.commands ?? [])
  } catch {
    // Offline or mid-reconnect. The SSE stream is the primary feed; a failed
    // refresh must never be louder than a dropped frame.
  }
}

// ── Wiring ────────────────────────────────────────────────────────────────────

let tickTimer: ReturnType<typeof setInterval> | null = null

export function toggleLiveCommands() {
  const next = !liveCommandsVisible
  setLiveCommandsVisible(next)
  cmdBtn?.classList.toggle('active', next)
  cmdLog?.classList.toggle('visible', next)
  if (tickTimer) { clearInterval(tickTimer); tickTimer = null }
  if (!next) return
  renderLiveCommands()
  refreshLiveCommands()
  // Ages tick locally so a running command visibly keeps running; only while
  // the view is open, so a closed panel costs nothing.
  tickTimer = setInterval(() => renderLiveCommands(), 1000)
}

/** The subscribe function sse.ts hands out — injected rather than imported. */
type SseSubscribe = (event: string, handler: (data: any) => void) => void

/**
 * Wire the button and, if given sse.ts's `onSse`, the two registry events.
 * The subscription is injected the same way wireServerEval's is, so this view
 * never imports the SSE module: an older server simply never sends either
 * frame, and `onSse` re-attaches handlers across reconnects for us.
 */
export function wireLiveCommandsEvents(onSse?: SseSubscribe) {
  cmdBtn?.addEventListener('click', toggleLiveCommands)
  onSse?.('commands', (data) => applyCommandsSnapshot(data))
  onSse?.('command_update', (data) => applyCommandUpdate(data))
}

/** Test seam: drop all registry state between cases. */
export function _resetLiveCommandsForTests() {
  clearCommandState()
  setLiveCommandsSupported(null)
  setLiveCommandsVisible(false)
  if (tickTimer) { clearInterval(tickTimer); tickTimer = null }
}
