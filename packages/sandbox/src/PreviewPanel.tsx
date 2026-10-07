import type { SandboxConfig } from './configs'
import type { EvalResponse } from './api'

/** What one configuration's evaluation of the current string produced. */
export interface ConfigResult {
  configId: string
  streamId?: string
  kind: 'idle' | 'loading' | 'ok' | 'refused' | 'error'
  /** The engine's own `fired` command id. The relay does not forward it on
   *  /eval, so the page fills this from the command log (see api.ts's
   *  inputActionsByStream) — but a response that does carry it still works. */
  fired?: string
  actions?: string[]
  reason?: string
  detail?: string
  relayMs?: number
  engineEvalNs?: number
}

export function rowFor(results: Record<string, ConfigResult>, id: string): ConfigResult {
  return results[id] ?? { configId: id, kind: 'idle' }
}

/** PREVIEW vs FIRE, stated to the reader in the UI itself.
 *
 *  Both buttons send the SAME string to the SAME route with the SAME manifest.
 *  The only difference is the stream id, and the server keys its entire
 *  preview/fire decision on that:
 *
 *    preview → stream `sandbox-preview-…` → evaluated for real, result NEVER
 *              delivered, and any deferred submit fire for that stream refused
 *    fire    → stream `sandbox-fire-…`    → delivered on the production path
 *
 *  Preview is not a client-side approximation and fire is not a special case:
 *  they are one code path with one marker between them. */
export function PreviewPanel({
  configs,
  results,
  onPreview,
  onFire,
  busy,
  text,
}: {
  configs: SandboxConfig[]
  results: Record<string, ConfigResult>
  onPreview: () => void
  onFire: (config: SandboxConfig) => void
  busy: boolean
  text: string
}) {
  return (
    <section className="preview" aria-label="preview and fire">
      <header className="preview-head">
        <h2>What would this send?</h2>
        <button className="primary" onClick={onPreview} disabled={busy || text.trim() === ''}>
          Preview (evaluate only)
        </button>
      </header>
      <p className="muted">
        Preview evaluates against the real compiled engine and shows the result — it never
        delivers an action, and the server refuses any deferred submit fire for a preview stream.
        Nothing is sent until you press Fire, and Fire only acts on the row you press it on.
      </p>

      <table>
        <thead>
          <tr>
            <th>configuration</th>
            <th>platform</th>
            <th>input action → output actions</th>
            <th>result</th>
            <th>fire the real action</th>
          </tr>
        </thead>
        <tbody>
          {configs.map((c) => {
            const r = rowFor(results, c.id)
            return (
              <tr key={c.id} data-config={c.id} data-kind={r.kind}>
                <td>{c.label}</td>
                <td>{c.platform || '(default)'}</td>
                <td>
                  <code>{r.fired || '—'}</code> → {r.actions && r.actions.length ? r.actions.join(', ') : '—'}
                </td>
                <td>
                  <Result result={r} />
                </td>
                <td>
                  <button
                    className="fire"
                    disabled={busy || text.trim() === '' || r.kind !== 'ok'}
                    onClick={() => onFire(c)}
                    title={
                      r.kind === 'ok'
                        ? `deliver this result now, through the production path (${r.actions?.join(', ') || 'no action'})`
                        : 'preview first'
                    }
                  >
                    Fire “{r.fired || r.actions?.[0] || 'this'}”
                  </button>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </section>
  )
}

function Result({ result }: { result: ConfigResult }) {
  if (result.kind === 'idle') return <span className="muted">preview to see</span>
  if (result.kind === 'loading') return <span className="muted">evaluating…</span>
  if (result.kind === 'error') return <span className="bad">{result.detail}</span>
  if (result.kind === 'refused') {
    return <span className="bad">refused ({result.reason}) — {result.detail}</span>
  }
  return (
    <span className="ok">
      matched{result.engineEvalNs ? ` · engine ${(result.engineEvalNs / 1000).toFixed(1)}µs` : ''}
      {result.relayMs != null ? ` · relay ${result.relayMs}ms` : ''}
    </span>
  )
}

/** decode turns one server evaluation response into the row state. Kept out of
 *  the component so the preview/fire distinction is testable without a DOM. */
export function decode(configId: string, res: EvalResponse, streamId: string, relayMs?: number): ConfigResult {
  if (res.refused) {
    return {
      configId,
      streamId,
      kind: 'refused',
      reason: res.refused,
      detail: res.hint ?? `${res.refused} is turned off`,
      relayMs,
    }
  }
  if (res.error) {
    return { configId, streamId, kind: 'error', detail: res.error, relayMs }
  }
  return {
    configId,
    streamId,
    kind: 'ok',
    fired: res.fired ?? '',
    actions: (res.actions ?? []).map((a) => a.verb),
    relayMs,
    engineEvalNs: res.engineEvalNs,
  }
}
