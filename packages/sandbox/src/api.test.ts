import { describe, expect, test } from 'bun:test'
import {
  FIRE_PREFIX,
  PREVIEW_PREFIX,
  buildEvalBody,
  evaluate,
  isPreviewStream,
  previewStreamId,
  fireStreamId,
  setOff,
} from './api'
import { EMPTY_FILTER, offTargetFor, readLog, toQuery, type ActionRecord } from './log'
import { DEFAULT_CONFIGS, configById } from './configs'

const config = configById(DEFAULT_CONFIGS, 'panel-default')!

describe('preview vs fire is a wire marker, not a flag', () => {
  test('the two stream ids carry distinct prefixes', () => {
    expect(previewStreamId('c')).toStartWith(PREVIEW_PREFIX)
    expect(fireStreamId('c')).toStartWith(FIRE_PREFIX)
    expect(previewStreamId('c')).not.toStartWith(FIRE_PREFIX)
    expect(isPreviewStream(previewStreamId('c'))).toBe(true)
    expect(isPreviewStream(fireStreamId('c'))).toBe(false)
  })

  test('each stream id is unique, so two evaluations never share engine state', () => {
    const ids = new Set([previewStreamId('c'), previewStreamId('c'), fireStreamId('c'), fireStreamId('c')])
    expect(ids.size).toBe(4)
  })
})

describe('buildEvalBody', () => {
  test('sends the chosen configuration as the per-request manifest override', () => {
    const body = buildEvalBody({ config, text: 'change inside input', streamId: previewStreamId('c'), device: 'd', version: 1 })
    // The engine's documented precedence is request > file > embedded, so this
    // is the real configuration being evaluated — not a client-side imitation.
    expect(body.commands).toBe(config.manifest)
    expect(body.platform).toBe('parlay')
    expect(body.voiceEnabled).toBe(true)
    expect(body.cursor).toEqual({ anchor: 19, active: 19 })
    expect(body.reason).toBe('sandbox-preview')
  })

  test('omits platform when the configuration does not name one', () => {
    const longform = configById(DEFAULT_CONFIGS, 'longform')
    const body = buildEvalBody({
      config: { ...longform!, platform: '' },
      text: 'x',
      streamId: fireStreamId('c'),
      device: 'd',
      version: 2,
    })
    expect(body.platform).toBeUndefined()
    expect(body.reason).toBe('sandbox-fire')
  })
})

describe('the log read', () => {
  test('sends only the axes the user set — an omitted filter must be absent, not empty', () => {
    expect(toQuery(EMPTY_FILTER)).toBe('')
    const qs = toQuery({ ...EMPTY_FILTER, inputAction: 'clear', outcome: 'refused', since: '15m', device: 'd1' })
    expect(qs).toContain('inputAction=clear')
    expect(qs).toContain('outcome=refused')
    expect(qs).toContain('since=15m')
    expect(qs).toContain('device=d1')
    expect(qs).not.toContain('reason=')
    expect(qs).not.toContain('source=')
  })

  test('GETs the real route with the query attached', async () => {
    const seen: string[] = []
    const fetchImpl = (async (url: string) => {
      seen.push(String(url))
      return new Response(
        JSON.stringify({ ok: true, total: 0, limit: 200, records: [], facets: {}, targets: [], outcomeVocabulary: [] }),
        { status: 200 },
      )
    }) as unknown as typeof fetch

    await readLog(fetchImpl, { ...EMPTY_FILTER, outcome: 'refused' })
    expect(seen[0]).toBe('/api/chat/action-log?outcome=refused')
  })

  test('surfaces a non-2xx rather than pretending the log is empty', async () => {
    const fetchImpl = (async () => new Response('', { status: 500 })) as unknown as typeof fetch
    await expect(readLog(fetchImpl, EMPTY_FILTER)).rejects.toThrow(/HTTP 500/)
  })
})

describe('evaluate and setOff', () => {
  test('POST the production routes with a JSON content type', async () => {
    const calls: { url: string; init: RequestInit }[] = []
    const fetchImpl = (async (url: string, init: RequestInit) => {
      calls.push({ url: String(url), init })
      return new Response(JSON.stringify({ ok: true }), { status: 200 })
    }) as unknown as typeof fetch

    await evaluate(fetchImpl, buildEvalBody({ config, text: 'x', streamId: 's', device: 'd', version: 1 }))
    await setOff(fetchImpl, { kind: 'action', id: 'clear', off: true })

    expect(calls[0].url).toBe('/api/chat/eval')
    expect(calls[1].url).toBe('/api/chat/off-switch')
    for (const c of calls) {
      expect((c.init.headers as Record<string, string>)['Content-Type']).toBe('application/json')
    }
    const off = JSON.parse(String(calls[1].init.body))
    expect(off).toMatchObject({ kind: 'action', id: 'clear', off: true, surface: 'website' })
  })

  test('a refusal is returned rather than thrown, because a refusal is an answer', async () => {
    const fetchImpl = (async () =>
      new Response(JSON.stringify({ ok: false, refused: 'connection', device: 'd1' }), { status: 200 })) as unknown as typeof fetch
    const res = await evaluate(fetchImpl, buildEvalBody({ config, text: 'x', streamId: 's', device: 'd', version: 1 }))
    expect(res.refused).toBe('connection')
  })

  test('a transport-level failure is thrown so the row can say so', async () => {
    const fetchImpl = (async () => new Response('{"error":"bad json"}', { status: 400 })) as unknown as typeof fetch
    await expect(evaluate(fetchImpl, buildEvalBody({ config, text: 'x', streamId: 's', device: 'd', version: 1 }))).rejects.toThrow(
      /HTTP 400/,
    )
  })
})

describe('offTargetFor', () => {
  const base: ActionRecord = { id: 'a', at: 't', source: 'test-site', outputActions: [], outcome: 'delivered' }

  test('offers the connection when the row has one', () => {
    expect(offTargetFor({ ...base, device: 'd1', inputAction: 'clear' })).toEqual({
      kind: 'connection',
      id: 'd1',
      label: 'connection d1',
    })
  })

  test('falls back to the action when there is no connection', () => {
    expect(offTargetFor({ ...base, inputAction: 'clear' })).toEqual({
      kind: 'action',
      id: 'clear',
      label: 'action clear',
    })
  })

  test('offers nothing rather than guessing when a row has neither', () => {
    expect(offTargetFor(base)).toBeNull()
  })
})
