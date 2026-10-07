/**
 * A fake parlay server and a stub SSE stream, for driving the sandbox page under
 * `bun test`. Split from App.test.tsx so that file is only assertions.
 *
 * The fake is STATEFUL on purpose: an evaluation appends a command-log row,
 * exactly as the real server does before it answers. That is what makes the
 * page's input-action join testable — the row's `streamId` is generated at
 * runtime by the page, so only a server that records what it was sent can answer
 * the question back.
 */

interface Call {
  url: string
  method: string
  body?: unknown
}

interface FakeRecord {
  id: string
  at: string
  source: string
  device: string
  streamId: string
  inputAction: string
  outputActions: string[]
  outcome: string
  reason?: string
}

/** A fake server for the three routes the page uses.
 *
 *  It is STATEFUL on purpose: an evaluation appends a command-log row, exactly
 *  as the real server does before it answers. That is what makes the page's
 *  input-action join testable — the row's `streamId` is generated at runtime by
 *  the page, so only a server that records what it was sent can answer the
 *  question back. */
export function fakeServer() {
  const calls: Call[] = []
  const records: FakeRecord[] = []
  let seq = 0

  const fetchImpl = (async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    calls.push({ url: String(url), method, body })

    if (String(url).startsWith('/api/chat/action-log')) {
      const newestFirst = [...records].reverse()
      return new Response(
        JSON.stringify({
          ok: true,
          total: newestFirst.length,
          limit: 200,
          records: newestFirst,
          facets: {
            sources: ['test-site'],
            inputActions: [...new Set(records.map((r) => r.inputAction))],
            outputActions: [...new Set(records.flatMap((r) => r.outputActions))],
            outcomes: [...new Set(records.map((r) => r.outcome))],
            reasons: [...new Set(records.map((r) => r.reason ?? '').filter(Boolean))],
            devices: ['dev-1'],
          },
          targets: [],
          outcomeVocabulary: ['delivered', 'queued', 'dropped', 'refused'],
        }),
        { status: 200 },
      )
    }
    if (String(url) === '/api/chat/off-switch') {
      return new Response(
        JSON.stringify({ ok: true, kind: body.kind, id: body.id, off: body.off, changed: true, targets: [] }),
        { status: 200 },
      )
    }
    if (String(url) === '/api/chat/eval') {
      const b = body as { streamId: string; device: string; commands: { commands: { id: string }[] } }
      const inputAction = b.commands.commands[0].id
      seq += 1
      records.push({
        id: `act-${seq}`,
        at: new Date(2026, 9, 7, 12, 0, seq).toISOString(),
        source: 'test-site',
        device: b.device,
        streamId: b.streamId,
        inputAction,
        outputActions: ['clear'],
        outcome: b.streamId.startsWith('sandbox-preview-') ? 'dropped' : 'delivered',
        reason: b.streamId.startsWith('sandbox-preview-') ? 'preview-suppressed' : undefined,
      })
      return new Response(JSON.stringify({ ok: true, actions: [{ verb: 'clear' }], engineEvalNs: 4200 }), { status: 200 })
    }
    return new Response('{}', { status: 404 })
  }) as unknown as typeof fetch

  return { calls, records, fetchImpl }
}

/** A stub EventSource that never connects — the delivered-action stream is
 *  driven explicitly by tests that care about it. */
export class StubEventSource {
  listeners: Record<string, ((e: MessageEvent) => void)[]> = {}
  constructor(public url: string) {}
  addEventListener(name: string, fn: (e: MessageEvent) => void) {
    ;(this.listeners[name] ??= []).push(fn)
  }
  removeEventListener(name: string, fn: (e: MessageEvent) => void) {
    this.listeners[name] = (this.listeners[name] ?? []).filter((f) => f !== fn)
  }
  close() {}
  emit(name: string, data: unknown) {
    for (const fn of this.listeners[name] ?? []) fn(new MessageEvent(name, { data: JSON.stringify(data) }))
  }
}
