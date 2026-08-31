// ── Remote debug log ─────────────────────────────────────────────────────────
// The captain reviews on his phone via Pulse and can't open devtools there.
// This shim captures window.onerror, unhandledrejection, console.error/warn,
// and explicit instrumented traces, batches them, and POSTs to the server so
// firstmate can tail a log file instead of asking for a screen recording.
//
// Toggle: localStorage 'pa-debug-log' = '0', or ?paDebug=0 in the URL — both
// disable. Default on. Initialized as early as possible (see init.ts) so it
// catches errors thrown during the rest of client init, not just post-load.
//
// The server endpoint (packages/server/src/debug-log.ts) is wired at
// POST /api/chat/debug-log. Against an older server without the route, a
// 404 is treated as a permanent no-op for the session (see
// `endpointUnavailable` below) rather than retrying every flush, so it stays
// harmless — no queued entries pile up, and it doesn't spam failed requests
// into the very on-screen console (mobile-console.ts) the captain would use
// to look at it.

import { CHAT_BASE } from './config'

export interface DebugEntry {
  ts: string
  level: 'error' | 'warn' | 'trace'
  source: string
  message: string
  detail?: unknown
}

const QUEUE_MAX = 50
const FLUSH_MS = 2000

let queue: DebugEntry[] = []
let flushTimer: ReturnType<typeof setTimeout> | null = null
// Set once a flush confirms the server route 404s (an older server without
// the route). Once true, flush() stops sending for the rest of the session
// instead of retrying every 2s forever.
let endpointUnavailable = false

function deviceId(): string {
  try {
    let id = localStorage.getItem('pa-device-id')
    if (!id) {
      id = crypto.randomUUID ? crypto.randomUUID() : `dev-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
      localStorage.setItem('pa-device-id', id)
    }
    return id
  } catch { return 'unknown' }
}

function isEnabled(): boolean {
  try {
    if (localStorage.getItem('pa-debug-log') === '0') return false
    if (new URLSearchParams(location.search).get('paDebug') === '0') return false
  } catch {}
  return true
}

function flush() {
  flushTimer = null
  if (!queue.length) return
  if (endpointUnavailable) { queue.length = 0; return }
  const entries = queue.splice(0, queue.length)
  const body = JSON.stringify({
    device: deviceId(),
    ua: navigator.userAgent,
    url: location.href,
    entries,
  })
  // keepalive so a flush queued right before navigation/reload still lands
  fetch(`${CHAT_BASE}/debug-log`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body,
    keepalive: true,
  }).then((res) => {
    // 404 means a server without the route — stop trying rather than
    // spamming failed requests into eruda's network tab every 2s.
    if (res.status === 404) endpointUnavailable = true
  }).catch(() => {})
}

function scheduleFlush() {
  if (flushTimer) return
  flushTimer = setTimeout(flush, FLUSH_MS)
}

function push(entry: DebugEntry) {
  if (!isEnabled()) return
  queue.push(entry)
  if (queue.length >= QUEUE_MAX) flush()
  else scheduleFlush()
}

// Explicit instrumented trace — `source` identifies the button/action so a
// batch of entries reads as a story ("pa-jump.click" → "scrollBottom" → ...).
export function logTrace(source: string, message: string, detail?: unknown) {
  push({ ts: new Date().toISOString(), level: 'trace', source, message, detail })
}

export function logError(source: string, message: string, detail?: unknown) {
  push({ ts: new Date().toISOString(), level: 'error', source, message, detail })
}

let initialized = false

export function initDebugLog(): void {
  if (initialized) return
  initialized = true

  window.addEventListener('error', (e) => {
    push({
      ts: new Date().toISOString(), level: 'error', source: 'window.onerror',
      message: e.message,
      detail: { filename: e.filename, lineno: e.lineno, colno: e.colno, stack: e.error?.stack },
    })
  })

  window.addEventListener('unhandledrejection', (e) => {
    const reason: any = e.reason
    push({
      ts: new Date().toISOString(), level: 'error', source: 'unhandledrejection',
      message: reason?.message ? String(reason.message) : String(reason),
      detail: { stack: reason?.stack },
    })
  })

  const origError = console.error.bind(console)
  const origWarn = console.warn.bind(console)
  console.error = (...args: unknown[]) => {
    origError(...args)
    push({ ts: new Date().toISOString(), level: 'error', source: 'console.error', message: args.map(String).join(' ') })
  }
  console.warn = (...args: unknown[]) => {
    origWarn(...args)
    push({ ts: new Date().toISOString(), level: 'warn', source: 'console.warn', message: args.map(String).join(' ') })
  }

  // Best-effort final flush — a beacon would survive unload better than fetch,
  // but keepalive fetch is good enough for a debug-only, best-effort channel.
  window.addEventListener('pagehide', flush)
}
