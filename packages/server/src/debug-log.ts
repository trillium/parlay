// ── Remote debug-log endpoint: POST /api/chat/debug-log ─────────────────────
//
// Client-side counterpart: packages/client/src/debug-log.ts POSTs batched
// console errors/warnings + instrumented traces here so the captain's phone
// (no devtools available) can be diagnosed by tailing a log file. Handler is
// intentionally framework-agnostic (`Request` in, `Response` out); router.ts
// dispatches it next to the other `${CHAT_BASE}` (`/api/chat`) routes.

import { appendFile, mkdir } from 'node:fs/promises'
import { dirname } from 'node:path'

const DEBUG_LOG_PATH =
  process.env.PARLAY_DEBUG_LOG_PATH ??
  `${process.env.PARLAY_STATE_HOME ?? `${process.env.HOME}/.parlay`}/debug.log`

const MAX_ENTRIES_PER_BATCH = 50
const MAX_FIELD_LEN = 4000

function isEnabled(): boolean {
  // Default on — set PARLAY_DEBUG_LOG=0 to disable without touching code.
  return process.env.PARLAY_DEBUG_LOG !== '0'
}

function truncate(s: string): string {
  return s.length > MAX_FIELD_LEN ? s.slice(0, MAX_FIELD_LEN) + '…(truncated)' : s
}

interface DebugLogEntry {
  ts?: string
  level?: string
  source?: string
  message?: string
  detail?: unknown
}

interface DebugLogBody {
  device?: string
  ua?: string
  url?: string
  entries?: DebugLogEntry[]
}

// Low-risk by design, not by enforcement: this trusts the network boundary
// (local/tailnet only — do not expose this port publicly) rather than
// authenticating requests, matching the brief's "keep it simple" ask.
export async function handleDebugLog(req: Request): Promise<Response> {
  if (!isEnabled()) return new Response(null, { status: 204 })

  let body: DebugLogBody
  try {
    body = await req.json()
  } catch {
    return new Response('invalid json', { status: 400 })
  }

  const entries = (Array.isArray(body.entries) ? body.entries : []).slice(0, MAX_ENTRIES_PER_BATCH)
  if (!entries.length) return new Response(null, { status: 204 })

  const device = truncate(String(body.device ?? '?'))
  const ua = truncate(String(body.ua ?? '?'))
  const url = truncate(String(body.url ?? '?'))

  const lines = entries.map((e) => {
    const ts = e.ts ?? new Date().toISOString()
    const level = String(e.level ?? 'trace').toUpperCase()
    const source = truncate(String(e.source ?? 'unknown'))
    const message = truncate(String(e.message ?? ''))
    let detail = ''
    if (e.detail !== undefined) {
      try { detail = ' ' + truncate(JSON.stringify(e.detail)) } catch { detail = ' (unserializable detail)' }
    }
    return `${ts} [${level}] device=${device} ua="${ua}" url=${url} source=${source} — ${message}${detail}`
  })

  try {
    await mkdir(dirname(DEBUG_LOG_PATH), { recursive: true })
    await appendFile(DEBUG_LOG_PATH, lines.join('\n') + '\n')
  } catch (err) {
    console.error('[debug-log] failed to write log file:', err)
    return new Response('failed to persist', { status: 500 })
  }

  return new Response(null, { status: 204 })
}
