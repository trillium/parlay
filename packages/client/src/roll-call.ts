// Client roll call: announce this page on connect, answer the server's
// roll_call order, and heartbeat. The server half is
// packages/go-server/internal/handlers/clients.go.
//
// A page reports what it knows; fields it cannot know are simply omitted. Other
// code (voice, the terminal bridge) contributes state through
// setRollCallState — merged, not replaced — so this module has no dependency on
// them.
import { CHAT_BASE } from './config'
import { PA_VERSION } from './version'

export interface RollCallState {
  version?: string
  build?: string
  device?: string
  deviceLabel?: string
  loadAddress?: string
  voiceArmed?: boolean
  lastPhrase?: string
  engineResult?: string
  submitOutcome?: string
  target?: string
  screenErrors?: string[]
}

export const HEARTBEAT_MS = 15_000

let extra: RollCallState = {}
let heartbeat: ReturnType<typeof setInterval> | null = null
const clientId = `c-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`

export function setRollCallState(patch: RollCallState) { extra = { ...extra, ...patch } }
export function resetRollCallState() { extra = {} }

/** Human name for this browser, e.g. "iPhone · Safari" or "Mac · Chrome". */
export function deviceLabel(ua = navigator.userAgent): string {
  const os = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android'
    : /Mac OS X/.test(ua) ? 'Mac' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : 'Unknown device'
  const br = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /(Chrome|CriOS)\//.test(ua) ? 'Chrome'
    : /Safari\//.test(ua) ? 'Safari' : 'browser'
  return `${os} · ${br}`
}

export function snapshot(device: string, banner = ''): RollCallState {
  const errors = [...(extra.screenErrors ?? [])]
  if (banner) errors.push(banner)
  return {
    version: PA_VERSION,
    build: PA_VERSION,
    loadAddress: location.href,
    ...extra,
    device,
    deviceLabel: deviceLabel(),
    ...(errors.length ? { screenErrors: errors } : {}),
  }
}

function post(kind: 'hello' | 'report' | 'heartbeat', state: RollCallState, rollCallId?: string) {
  return fetch(`${CHAT_BASE}/clients/report`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ clientId, kind, rollCallId, state }),
  }).catch(() => {})
}

/** Call on every (re)connect of the events stream. */
export function announce(es: EventSource, device: string, banner: () => string) {
  void post('hello', snapshot(device, banner()))
  es.addEventListener('roll_call', (e: MessageEvent) => {
    let id = ''
    try { id = JSON.parse(e.data).id ?? '' } catch {}
    void post('report', snapshot(device, banner()), id)
  })
  if (heartbeat) clearInterval(heartbeat)
  heartbeat = setInterval(() => { void post('heartbeat', {}) }, HEARTBEAT_MS)
}
