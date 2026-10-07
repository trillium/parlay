/**
 * The COMMAND LOG's client half: the record shape, the filter vocabulary, the
 * read, and the two small helpers the page needs to point its per-row off switch
 * at the right target.
 *
 * Split from api.ts, which owns the evaluation up-channel (preview and fire) and
 * the off switch's write. The log is a read surface; keeping the two apart keeps
 * the preview/fire markers in one file and the filter vocabulary in another.
 */

export interface ActionRecord {
  id: string
  at: string
  source: string
  device?: string
  streamId?: string
  inputAction?: string
  outputActions: string[]
  outcome: string
  reason?: string
  relayMs?: number
  engineEvalNs?: number
}

export interface OffTarget {
  kind: string
  id: string
  by?: string
  surface?: string
  at: string
}

export interface ActionLogFacets {
  sources: string[]
  inputActions: string[]
  outputActions: string[]
  outcomes: string[]
  reasons: string[]
  devices: string[]
}

export interface ActionLogResponse {
  ok: boolean
  total: number
  limit: number
  records: ActionRecord[]
  facets: ActionLogFacets
  targets: OffTarget[]
  outcomeVocabulary: string[]
  error?: string
}

/** The filter axes the server implements. Kept as a list so the page renders
 *  controls for what exists instead of a free-text box per guessed field. */
export const FILTER_FIELDS = ['source', 'inputAction', 'outputAction', 'outcome', 'reason', 'device'] as const

export type FilterField = (typeof FILTER_FIELDS)[number]

export interface LogFilter {
  source: string
  inputAction: string
  outputAction: string
  outcome: string
  reason: string
  device: string
  since: string
  until: string
  limit: string
}

export const EMPTY_FILTER: LogFilter = {
  source: '',
  inputAction: '',
  outputAction: '',
  outcome: '',
  reason: '',
  device: '',
  since: '',
  until: '',
  limit: '',
}

/** toQuery builds the query string, sending only the axes the user set. An
 *  omitted filter must be ABSENT rather than empty, or the server would filter
 *  on the empty string and return nothing. */
export function toQuery(filter: LogFilter): string {
  const params = new URLSearchParams()
  for (const key of [...FILTER_FIELDS, 'since', 'until', 'limit'] as const) {
    const value = filter[key as keyof LogFilter]
    if (value) params.set(key, value)
  }
  return params.toString()
}

export async function readLog(fetchImpl: typeof fetch, filter: LogFilter, server = ''): Promise<ActionLogResponse> {
  const qs = toQuery(filter)
  const res = await fetchImpl(`${server}/api/chat/action-log${qs ? `?${qs}` : ''}`, {
    headers: { accept: 'application/json' },
  })
  if (!res.ok) throw new Error(`/api/chat/action-log: HTTP ${res.status}`)
  return (await res.json()) as ActionLogResponse
}

/** inputActionsByStream joins log rows to a stream id → the input action each one
 *  resolved to.
 *
 *  The relay's own evaluation response does not carry the engine's `fired`
 *  command id (the engine envelope does; the relay drops it), and the sandbox
 *  must not extend a public endpoint's shape to get it. It does not have to: the
 *  command log records exactly that field, keyed by the same stream id this page
 *  invented for the evaluation. So the page reads the answer back from the log it
 *  already displays — which also means the table and the log can never disagree
 *  about what a string resolved to. */
export function inputActionsByStream(records: ActionRecord[]): Map<string, string> {
  const out = new Map<string, string>()
  for (const rec of records) {
    if (rec.streamId && rec.inputAction && !out.has(rec.streamId)) {
      out.set(rec.streamId, rec.inputAction)
    }
  }
  return out
}

/** offTargetFor names what ONE log row's off switch would turn off.
 *
 *  A row carries both a connection (its device) and, when it resolved to
 *  something, an action (its input action). The row offers the connection when
 *  it has one, because that is the coarser and more useful kill; the action is
 *  reachable from the row's own input-action cell. Returning null rather than a
 *  guess is deliberate — a switch that silently aimed at the wrong target would
 *  be worse than one that is not offered. */
export function offTargetFor(rec: ActionRecord): { kind: string; id: string; label: string } | null {
  if (rec.device) return { kind: 'connection', id: rec.device, label: `connection ${rec.device}` }
  if (rec.inputAction) return { kind: 'action', id: rec.inputAction, label: `action ${rec.inputAction}` }
  return null
}
