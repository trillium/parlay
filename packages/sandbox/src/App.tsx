import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { loadConfigs, type SandboxConfig } from './configs'
import { buildEvalBody, evaluate, fireStreamId, previewStreamId, type ParlayAction } from './api'
import { EMPTY_FILTER, inputActionsByStream, readLog } from './log'
import { decode, PreviewPanel, rowFor, type ConfigResult } from './PreviewPanel'
import { LogPanel } from './LogPanel'

/** The sandbox's device id. It is a real connection as far as the server is
 *  concerned — that is what makes the row-level off switch meaningful and what
 *  lets the log show which connection an evaluation came from. Persisted like
 *  the production wrapper's, so reloads keep one identity. */
function sandboxDevice(): string {
  const key = 'parlay-sandbox-device'
  const existing = globalThis.localStorage?.getItem(key)
  if (existing) return existing
  const id = `sandbox-${Math.random().toString(36).slice(2, 10)}`
  globalThis.localStorage?.setItem(key, id)
  return id
}

export interface AppProps {
  /** Injectable for tests; the browser's global otherwise. */
  fetchImpl?: typeof fetch
  /** Injectable EventSource so a test can drive the delivered-action stream. */
  eventSourceFactory?: (url: string) => EventSource
  /** Base URL of the parlay server. Empty = same origin (the dev proxy or the
   *  go-server's own static mount), which is also what keeps this page inside
   *  the origin guard. */
  server?: string
  device?: string
}

export function App({ fetchImpl, eventSourceFactory, server = '', device }: AppProps) {
  const doFetch = fetchImpl ?? globalThis.fetch.bind(globalThis)
  const dev = useMemo(() => device ?? sandboxDevice(), [device])

  const [configs, setConfigs] = useState<SandboxConfig[]>([])
  const [configNote, setConfigNote] = useState('')
  const [text, setText] = useState('')
  const [results, setResults] = useState<Record<string, ConfigResult>>({})
  const [busy, setBusy] = useState(false)
  const [refreshToken, setRefreshToken] = useState(0)
  const [delivered, setDelivered] = useState<{ streamId: string; actions: ParlayAction[] }[]>([])
  const [actionOff, setActionOff] = useState<string[]>([])
  const version = useRef(0)

  useEffect(() => {
    let cancelled = false
    void loadConfigs(doFetch).then((loaded) => {
      if (cancelled) return
      setConfigs(loaded.configs)
      setConfigNote(loaded.error ? `using built-in configurations (${loaded.error})` : `configurations from ${loaded.source}`)
    })
    return () => {
      cancelled = true
    }
  }, [doFetch])

  // The production down-channel: one shared SSE connection carrying
  // `input_action`. A FIRE's result arrives here, exactly as it would for a live
  // panel — which is what makes "fires the real corresponding action through the
  // same path production uses" checkable rather than asserted.
  useEffect(() => {
    const factory = eventSourceFactory ?? (globalThis.EventSource ? (u: string) => new EventSource(u) : undefined)
    if (!factory) return
    const es = factory(`${server}/api/chat/events?device=${encodeURIComponent(dev)}`)
    const onAction = (event: MessageEvent) => {
      try {
        const data = JSON.parse(event.data) as { streamId?: string; actions?: ParlayAction[] }
        setDelivered((prev) => [{ streamId: data.streamId ?? '', actions: data.actions ?? [] }, ...prev].slice(0, 20))
      } catch {
        /* a malformed frame is the server's business, not this page's */
      }
    }
    es.addEventListener('input_action', onAction as EventListener)
    return () => {
      es.removeEventListener('input_action', onAction as EventListener)
      es.close()
    }
  }, [dev, eventSourceFactory, server])

  const runAll = useCallback(
    async (kind: 'preview' | 'fire') => {
      const target = text
      if (target.trim() === '') return
      setBusy(true)
      setResults((prev) => {
        const next = { ...prev }
        for (const c of configs) next[c.id] = { configId: c.id, kind: 'loading' }
        return next
      })
      await Promise.all(
        configs.map(async (config) => {
          version.current += 1
          const streamId = kind === 'preview' ? previewStreamId(config.id) : fireStreamId(config.id)
          const started = performance.now()
          try {
            const res = await evaluate(
              doFetch,
              buildEvalBody({ config, text: target, streamId, device: dev, version: version.current }),
              server,
            )
            setResults((prev) => ({ ...prev, [config.id]: decode(config.id, res, streamId, Math.round(performance.now() - started)) }))
          } catch (err) {
            setResults((prev) => ({
              ...prev,
              [config.id]: { configId: config.id, streamId, kind: 'error', detail: String(err) },
            }))
          }
        }),
      )
      setBusy(false)
      // Fill each row's "input action" from the command log, keyed by the stream
      // id this page invented for that evaluation. The relay's /eval response
      // does not carry the engine's `fired` command id, and extending a public
      // endpoint's shape to get it would be the wrong trade — the log already
      // records exactly that field for exactly this stream. An unreadable log is
      // not an error here: the row still shows the output actions.
      if (kind === 'preview') {
        try {
          const byStream = inputActionsByStream((await readLog(doFetch, EMPTY_FILTER, server)).records)
          setResults((prev) => {
            const next = { ...prev }
            for (const [id, row] of Object.entries(next)) {
              const fired = row.streamId ? byStream.get(row.streamId) : undefined
              if (fired) next[id] = { ...row, fired }
            }
            return next
          })
        } catch {
          /* the join is an enhancement; the verbs are the answer either way */
        }
      }
      setRefreshToken((n) => n + 1)
    },
    [configs, dev, doFetch, server, text],
  )

  const fireOne = useCallback(
    async (config: SandboxConfig) => {
      setBusy(true)
      version.current += 1
      const streamId = fireStreamId(config.id)
      try {
        const res = await evaluate(
          doFetch,
          buildEvalBody({ config, text, streamId, device: dev, version: version.current }),
          server,
        )
        setResults((prev) => ({ ...prev, [config.id]: decode(config.id, res, streamId) }))
      } catch (err) {
        setResults((prev) => ({ ...prev, [config.id]: { configId: config.id, streamId, kind: 'error', detail: String(err) } }))
      }
      setBusy(false)
      setRefreshToken((n) => n + 1)
    },
    [configs, dev, doFetch, server, text],
  )

  return (
    <main className="sandbox">
      <h1>Parlay sandbox</h1>
      <p className="muted" data-testid="device">
        device <code>{dev}</code> · {configNote || 'loading configurations…'}
      </p>

      <label className="composer">
        <span>test string</span>
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="change inside input · send it · go to mayor"
          aria-label="test string"
        />
      </label>

      <PreviewPanel
        configs={configs}
        results={results}
        busy={busy}
        text={text}
        onPreview={() => void runAll('preview')}
        onFire={(c) => void fireOne(c)}
      />

      <section aria-label="delivered actions">
        <h2>Delivered over SSE</h2>
        <p className="muted">
          What the server actually broadcast to this connection. A preview never appears here; a
          fire does.
        </p>
        <ul>
          {delivered.map((d, i) => (
            <li key={`${d.streamId}-${i}`}>
              <code>{d.streamId}</code> → {d.actions.map((a) => a.verb).join(', ') || '—'}
            </li>
          ))}
          {delivered.length === 0 && <li className="muted">nothing delivered yet</li>}
        </ul>
      </section>

      <LogPanel
        fetchImpl={doFetch}
        server={server}
        refreshToken={refreshToken}
        onActionOff={(id) => setActionOff((prev) => (prev.includes(id) ? prev : [...prev, id]))}
      />

      {actionOff.length > 0 && (
        <p className="note warn" data-testid="actions-off">
          actions turned off in this session: {actionOff.join(', ')}
        </p>
      )}

      {configs.length > 0 && (
        <details>
          <summary>loaded configurations</summary>
          <ul>
            {configs.map((c) => (
              <li key={c.id}>
                <strong>{c.label}</strong> — {rowFor(results, c.id).kind} · {c.manifest.commands.length} commands
              </li>
            ))}
          </ul>
        </details>
      )}
    </main>
  )
}
