import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, describe, beforeEach } from 'bun:test'
import { join } from 'node:path'
import {
  applyCommandsSnapshot, applyCommandUpdate,
  sortCommands, isRunning, commandAge, commandDetail, liveDurationMs,
  commandRowEl, renderCommandsInto, wireLiveCommandsEvents,
  COVERAGE_NOTE, UNSUPPORTED_NOTE,
  _resetLiveCommandsForTests,
  type CommandsResponse, type CommandInvocation,
} from './live-commands'
import { liveCommands, setLiveCommandsSupported } from './state'

// ── The cross-surface contract ────────────────────────────────────────────────
// This fixture is owned by the server (packages/go-server/testdata) and is read
// by three test suites: the Go handler's, the Go CLI's, and this one. It is the
// mechanism behind "one registry, two renderers" — if the server ever changes
// the wire shape, the panel's test fails in the same commit as the server's.
const GOLDEN = join(import.meta.dir, '../../go-server/testdata/live-commands.golden.json')

async function golden(): Promise<CommandsResponse> {
  return await Bun.file(GOLDEN).json()
}

function host(): HTMLElement {
  const el = document.createElement('div')
  document.body.appendChild(el)
  return el
}

beforeEach(() => {
  _resetLiveCommandsForTests()
  document.body.innerHTML = ''
})

describe('the golden fixture is the same state the CLI renders', () => {
  test('every record and field survives into the panel model', async () => {
    const body = await golden()
    applyCommandsSnapshot(body.commands)

    expect(liveCommands.size).toBe(5)
    expect([...liveCommands.values()].filter(isRunning).length).toBe(body.running)

    const listen = liveCommands.get('cmd-5') as CommandInvocation
    expect(listen.verb).toBe('listen')
    expect(listen.agent).toBe('crew-1')
    expect(listen.flags).toEqual(['--agent', '--caps'])
    expect(listen.pid).toBe(4242)

    const failed = liveCommands.get('cmd-3') as CommandInvocation
    expect(failed.exitCode).toBe(3)
    expect(failed.outcome).toBe('error')
  })

  test('the rendered rows show the same columns the CLI table shows', async () => {
    const body = await golden()
    applyCommandsSnapshot(body.commands)

    const el = host()
    renderCommandsInto(el, Date.parse('2026-01-01T12:00:30Z'))
    const text = el.textContent ?? ''

    expect(text).toContain('2 running (5 tracked)')
    for (const want of ['listen', 'crew-1', '--agent --caps', 'merge-gate', 'exit 3 error', 'no-heartbeat']) {
      expect(text).toContain(want)
    }
  })
})

describe('ordering', () => {
  test('running commands come first, newest start first', async () => {
    const { commands } = await golden()
    const ids = sortCommands(commands).map(c => c.id)
    expect(ids.slice(0, 2)).toEqual(['cmd-5', 'cmd-4'])
  })

  test('terminal commands follow, most recently ended first', async () => {
    const { commands } = await golden()
    const ids = sortCommands(commands).map(c => c.id)
    expect(ids.slice(2)).toEqual(['cmd-3', 'cmd-2', 'cmd-1'])
  })
})

describe('live updates', () => {
  const running = (id: string): CommandInvocation => ({
    id, verb: 'listen', state: 'running',
    startedAt: '2026-01-01T12:00:00Z', updatedAt: '2026-01-01T12:00:00Z', durationMs: 0,
  })

  test('a command that starts appears', () => {
    applyCommandUpdate(running('c-1'))
    expect(liveCommands.has('c-1')).toBe(true)
  })

  test('a command that finishes stops being running but stays visible', () => {
    applyCommandUpdate(running('c-1'))
    applyCommandUpdate({ ...running('c-1'), state: 'finished', endedAt: '2026-01-01T12:00:04Z', exitCode: 0, outcome: 'ok', durationMs: 4000 })

    const rec = liveCommands.get('c-1') as CommandInvocation
    expect(isRunning(rec)).toBe(false)
    expect(commandDetail(rec)).toBe('exit 0 ok')
  })

  test('a reaped command is shown as expired, then dropped when the server drops it', () => {
    applyCommandUpdate(running('c-1'))
    applyCommandUpdate({ ...running('c-1'), state: 'expired', outcome: 'no-heartbeat', endedAt: '2026-01-01T12:01:30Z', durationMs: 90000 })
    expect((liveCommands.get('c-1') as CommandInvocation).state).toBe('expired')

    // The server's prune notice carries only an id and the `dropped` state.
    applyCommandUpdate({ id: 'c-1', state: 'dropped' } as CommandInvocation)
    expect(liveCommands.has('c-1')).toBe(false)
  })

  test('a snapshot replaces the registry rather than merging into it', async () => {
    applyCommandUpdate(running('gone'))
    const { commands } = await golden()
    applyCommandsSnapshot(commands)
    expect(liveCommands.has('gone')).toBe(false)
    expect(liveCommands.size).toBe(5)
  })

  test('a malformed frame is ignored, not thrown', () => {
    applyCommandUpdate(undefined as any)
    applyCommandUpdate({ verb: 'no-id' } as any)
    applyCommandsSnapshot(undefined as any)
    expect(liveCommands.size).toBe(0)
  })
})

describe('ages', () => {
  test('formatting matches the CLI, and a skewed clock never reads negative', () => {
    expect(commandAge(0)).toBe('0.0s')
    expect(commandAge(1500)).toBe('1.5s')
    expect(commandAge(59900)).toBe('59.9s')
    expect(commandAge(60000)).toBe('1m00s')
    expect(commandAge(132000)).toBe('2m12s')
    expect(commandAge(3600000)).toBe('1h00m')
    expect(commandAge(5400000)).toBe('1h30m')
    expect(commandAge(-1000000)).toBe('0.0s')
  })

  test('a running age keeps ticking between server updates; a finished one is frozen', () => {
    const t0 = 1_000_000
    applyCommandUpdate({
      id: 'c-1', verb: 'listen', state: 'running',
      startedAt: 'x', updatedAt: 'x', durationMs: 5000,
    }, t0)
    const rec = liveCommands.get('c-1') as CommandInvocation
    expect(liveDurationMs(rec, t0)).toBe(5000)
    expect(liveDurationMs(rec, t0 + 3000)).toBe(8000)

    applyCommandUpdate({ ...rec, state: 'finished', durationMs: 6000 }, t0)
    const done = liveCommands.get('c-1') as CommandInvocation
    expect(liveDurationMs(done, t0 + 60_000)).toBe(6000)
  })
})

// ── Redaction ─────────────────────────────────────────────────────────────────
// The server stores no free-form text, so the panel has none to render. These
// pin that the panel does not reintroduce any: it shows flag NAMES and an
// outcome token, nothing else, and it escapes what it shows.

describe('what the panel is allowed to show', () => {
  test('a running row shows flag names and nothing that could be a value', async () => {
    const { commands } = await golden()
    for (const rec of commands) {
      const detail = commandDetail(rec)
      if (isRunning(rec)) {
        for (const token of detail.split(' ').filter(Boolean)) {
          expect(token.startsWith('-')).toBe(true)
          expect(token).not.toContain('=')
        }
      } else {
        expect(detail).toMatch(/^(exit -?\d+)?( ?[a-z0-9-]+)?$/)
      }
    }
  })

  test('a flagless running command shows an empty detail rather than a guess', () => {
    expect(commandDetail({ id: 'x', verb: 'history', state: 'running', startedAt: '', updatedAt: '', durationMs: 0 })).toBe('')
  })

  test('a hostile verb cannot inject markup', () => {
    const el = commandRowEl({
      id: 'x', verb: '<img src=x onerror=alert(1)>', state: 'running',
      startedAt: '', updatedAt: '', durationMs: 0,
    })
    expect(el.querySelector('img')).toBeNull()
    expect(el.textContent).toContain('<img src=x onerror=alert(1)>')
  })
})

// ── Wiring ────────────────────────────────────────────────────────────────────
// The view never imports sse.ts; init.ts injects its `onSse`, the same way
// wireServerEval's subscription is injected. That is what keeps this module
// out of the SSE module's import graph — so pin that both events get wired.

describe('wiring', () => {
  test('subscribes to both registry events, and each one lands', () => {
    const subs = new Map<string, (data: any) => void>()
    wireLiveCommandsEvents((event, handler) => subs.set(event, handler))
    expect([...subs.keys()].sort()).toEqual(['command_update', 'commands'])

    subs.get('commands')!([{ id: 'c-1', verb: 'listen', state: 'running', startedAt: '', updatedAt: '', durationMs: 0 }])
    expect(liveCommands.has('c-1')).toBe(true)

    subs.get('command_update')!({ id: 'c-1', state: 'dropped' })
    expect(liveCommands.has('c-1')).toBe(false)
  })

  test('wiring without an SSE subscription is a no-op, not a throw', () => {
    expect(() => wireLiveCommandsEvents()).not.toThrow()
  })
})

// ── Degrading quietly ─────────────────────────────────────────────────────────

describe('a server without the registry', () => {
  test('renders as unavailable, not as an error, and shows no rows', () => {
    setLiveCommandsSupported(false)
    const el = host()
    renderCommandsInto(el)

    expect(el.textContent).toContain(UNSUPPORTED_NOTE)
    expect(el.querySelectorAll('.pa-cmd-row').length).toBe(0)
  })

  test('an empty registry says what it cannot see, so silence is never read as "nothing is running"', () => {
    setLiveCommandsSupported(true)
    const el = host()
    renderCommandsInto(el)

    expect(el.textContent).toContain('No commands are reporting.')
    expect(el.textContent).toContain(COVERAGE_NOTE)
  })
})
