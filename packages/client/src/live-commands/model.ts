// The live-command registry's client-side model: the wire shape, the pure
// display rules, and the ingest that keeps `liveCommands` current. No DOM, no
// network — panel.ts owns both, so everything here is testable as data.
//
// The server owns the registry itself (packages/go-server, GET
// /api/chat/commands + the `commands` / `command_update` SSE events); this
// file only mirrors it. `parlay commands` renders the SAME server state in a
// terminal — one registry, two renderers — so the two views can never
// disagree about what is running. The shared wire-shape fixture both surfaces
// are tested against is packages/go-server/testdata/live-commands.golden.json.

import { liveCommands, setLiveCommandsSupported } from '../state'

export interface CommandInvocation {
  id:          string
  verb:        string
  agent?:      string
  channel?:    string
  flags?:      string[]
  pid?:        number
  state:       'running' | 'finished' | 'failed' | 'expired' | 'dropped' | string
  startedAt:   string
  updatedAt:   string
  endedAt?:    string
  exitCode?:   number
  outcome?:    string
  durationMs:  number
}

export interface CommandsResponse {
  ok:            boolean
  now:           string
  running:       number
  staleAfterMs:  number
  commands:      CommandInvocation[]
}

// COVERAGE_NOTE is deliberately the same claim the CLI's empty state makes.
// If the two ever drift, one of the surfaces is lying about its blind spots.
export const COVERAGE_NOTE =
  'Only the Go CLI reports itself — shell wrappers, the TS CLI, server-side work, and a bare `parlay` are not tracked.'

export const UNSUPPORTED_NOTE =
  'This server does not expose a live-command registry. Nothing is broken; there is simply nothing to read.'

// receivedAt lets a record's age keep ticking between server updates without
// trusting the browser's clock to agree with the server's: the server sends a
// durationMs it measured itself, and we only ever ADD locally-measured
// elapsed time to it. Clock skew therefore cannot make an age go backwards.
const receivedAt = new Map<string, number>()

// ── Ingest ────────────────────────────────────────────────────────────────────

/** Replace the whole registry — the `commands` SSE burst and the read endpoint. */
export function ingestSnapshot(list: CommandInvocation[], now = Date.now()) {
  liveCommands.clear()
  receivedAt.clear()
  for (const rec of list ?? []) {
    if (!rec || !rec.id) continue
    liveCommands.set(rec.id, rec)
    receivedAt.set(rec.id, now)
  }
  setLiveCommandsSupported(true)
}

/**
 * Upsert one record — the `command_update` SSE event. `dropped` is the
 * server's notice that a terminal record has aged out of the registry, so a
 * long-lived panel prunes it instead of growing forever.
 */
export function ingestUpdate(rec: CommandInvocation, now = Date.now()) {
  if (!rec || !rec.id) return
  if (rec.state === 'dropped') {
    liveCommands.delete(rec.id)
    receivedAt.delete(rec.id)
    return
  }
  liveCommands.set(rec.id, rec)
  receivedAt.set(rec.id, now)
  setLiveCommandsSupported(true)
}

/** Drop every locally-held record. Used by the test reset seam. */
export function clearCommandState() {
  liveCommands.clear()
  receivedAt.clear()
}

// ── Pure display rules ────────────────────────────────────────────────────────

export function isRunning(rec: CommandInvocation): boolean {
  return rec.state === 'running'
}

/** Running first (newest start first), then terminal (most recently ended first). */
export function sortCommands(list: CommandInvocation[]): CommandInvocation[] {
  return [...list].sort((a, b) => {
    if (isRunning(a) !== isRunning(b)) return isRunning(a) ? -1 : 1
    const key = isRunning(a)
      ? (r: CommandInvocation) => r.startedAt || ''
      : (r: CommandInvocation) => r.endedAt || r.updatedAt || ''
    return key(b).localeCompare(key(a))
  })
}

/**
 * How long this invocation has been going, in ms. A running record keeps
 * ticking locally between server updates; a terminal one is frozen at the
 * duration the server measured.
 */
export function liveDurationMs(rec: CommandInvocation, now = Date.now()): number {
  const base = rec.durationMs ?? 0
  if (!isRunning(rec)) return base
  const since = receivedAt.get(rec.id)
  return since === undefined ? base : base + Math.max(0, now - since)
}

/** Same age format the CLI uses, so a screenshot of either view reads alike. */
export function commandAge(ms: number): string {
  const s = Math.max(0, ms) / 1000
  if (s < 60) return `${s.toFixed(1)}s`
  if (s < 3600) return `${Math.floor(s / 60)}m${String(Math.floor(s % 60)).padStart(2, '0')}s`
  return `${Math.floor(s / 3600)}h${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}m`
}

/**
 * The one free-form-looking column, which is why it is careful: for a running
 * command it shows flag NAMES (the server stores no values), and for a
 * terminal one the exit code and outcome token. It never invents text.
 */
export function commandDetail(rec: CommandInvocation): string {
  if (isRunning(rec)) return rec.flags?.length ? rec.flags.join(' ') : ''
  const bits: string[] = []
  if (typeof rec.exitCode === 'number') bits.push(`exit ${rec.exitCode}`)
  if (rec.outcome) bits.push(rec.outcome)
  return bits.join(' ')
}
