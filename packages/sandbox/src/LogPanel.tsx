import { useCallback, useEffect, useState } from 'react'
import {
  EMPTY_FILTER,
  FILTER_FIELDS,
  readLog,
  type ActionLogResponse,
  type FilterField,
  type LogFilter,
  type OffTarget,
} from './log'
import { setOff } from './api'
import { OffStrip, Row, effectOf } from './LogRows'

/** The filter axes, with the label and the help text a reader needs to know what
 *  a value would even look like. `outcome` is a select over the server's closed
 *  vocabulary; every other axis is populated from the values the server reports
 *  it actually holds (the facets), so the controls can only offer real choices. */
const AXIS_LABELS: Record<FilterField, string> = {
  source: 'input source',
  inputAction: 'input action',
  outputAction: 'output action',
  outcome: 'outcome',
  reason: 'why',
  device: 'connection',
}

const TIME_PRESETS = [
  { label: 'all time', value: '' },
  { label: 'last 1m', value: '1m' },
  { label: 'last 15m', value: '15m' },
  { label: 'last 1h', value: '1h' },
]

/** facetValues returns the server's present values for one axis. `outcome` is
 *  special: its vocabulary is closed and complete, so an outcome with no rows
 *  yet is still offered — a filter that hides its own axis is decoration. */
function facetValues(facets: ActionLogResponse['facets'] | null, axis: FilterField): string[] {
  if (!facets) return []
  switch (axis) {
    case 'source':
      return facets.sources
    case 'inputAction':
      return facets.inputActions
    case 'outputAction':
      return facets.outputActions
    case 'reason':
      return facets.reasons
    case 'device':
      return facets.devices
    default:
      return []
  }
}

export interface LogPanelProps {
  fetchImpl: typeof fetch
  server?: string
  /** Bumped by the page when it has just fired something, so the log re-reads. */
  refreshToken: number
  onActionOff?: (id: string) => void
}

export function LogPanel({ fetchImpl, server, refreshToken, onActionOff }: LogPanelProps) {
  const [filter, setFilter] = useState<LogFilter>(EMPTY_FILTER)
  const [data, setData] = useState<ActionLogResponse | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')

  const refresh = useCallback(async () => {
    setBusy(true)
    try {
      setData(await readLog(fetchImpl, filter, server))
      setError('')
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }, [fetchImpl, filter, server])

  // Re-read on every filter change and on every fire the page performed. The
  // FILTERING ITSELF IS THE SERVER'S — this panel never re-implements it, which
  // is what makes the view and the API agree by construction.
  useEffect(() => {
    void refresh()
  }, [refresh, refreshToken])

  const turnOff = async (target: { kind: string; id: string }) => {
    setNote('')
    try {
      const res = await setOff(fetchImpl, { ...target, off: true }, server)
      setNote(`turned ${res.kind} ${res.id} OFF — ${effectOf(res.kind)}`)
      if (target.kind === 'action') onActionOff?.(target.id)
      await refresh()
    } catch (err) {
      setError(String(err))
    }
  }

  const restore = async (target: OffTarget) => {
    setNote('')
    try {
      const res = await setOff(fetchImpl, { kind: target.kind, id: target.id, off: false }, server)
      setNote(`${res.kind} ${res.id} is back ON`)
      await refresh()
    } catch (err) {
      setError(String(err))
    }
  }

  return (
    <section className="log" aria-label="command log">
      <header className="log-head">
        <h2>Command log</h2>
        <button onClick={() => void refresh()} disabled={busy}>
          {busy ? 'reading…' : 'refresh'}
        </button>
        <span className="muted">
          {data ? `${data.records.length} of ${data.total} rows` : 'not read yet'}
        </span>
      </header>

      {note && <p className="note ok">{note}</p>}
      {error && <p className="note bad">{error}</p>}

      <OffStrip targets={data?.targets ?? []} onRestore={restore} />

      <div className="filters">
        {FILTER_FIELDS.map((axis) => (
          <label key={axis}>
            <span>{AXIS_LABELS[axis]}</span>
            <select
              value={filter[axis]}
              onChange={(e) => setFilter({ ...filter, [axis]: e.target.value })}
              aria-label={`filter by ${AXIS_LABELS[axis]}`}
            >
              <option value="">any</option>
              {(axis === 'outcome' ? (data?.outcomeVocabulary ?? []) : facetValues(data?.facets ?? null, axis)).map(
                (v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ),
              )}
            </select>
          </label>
        ))}
        <label>
          <span>time window</span>
          <select value={filter.since} onChange={(e) => setFilter({ ...filter, since: e.target.value })}>
            {TIME_PRESETS.map((p) => (
              <option key={p.value} value={p.value}>
                {p.label}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>why (free text)</span>
          <input
            value={filter.reason}
            onChange={(e) => setFilter({ ...filter, reason: e.target.value })}
            placeholder="no-match, no-subscriber, off-action…"
          />
        </label>
        <button onClick={() => setFilter(EMPTY_FILTER)}>clear filters</button>
      </div>

      <table>
        <thead>
          <tr>
            <th>when</th>
            <th>source</th>
            <th>outcome</th>
            <th>why</th>
            <th>input action → output actions</th>
            <th>connection</th>
            <th>off</th>
          </tr>
        </thead>
        <tbody>
          {(data?.records ?? []).map((rec) => (
            <Row key={rec.id} rec={rec} onTurnOff={turnOff} targets={data?.targets ?? []} />
          ))}
          {data && data.records.length === 0 && (
            <tr>
              <td colSpan={7} className="muted">
                no rows match these filters. The log is bounded to the recent past, so an empty
                result usually means the filter — not that nothing ran.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </section>
  )
}
