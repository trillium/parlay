import { useCallback, useState } from 'react'

/** One page that announced itself to the server (client roll call). Fields a
 *  page never reported are absent, and rendered as "—", never as a default. */
export interface ClientRow {
  clientId: string
  stale: boolean
  lastSeen: string
  state: {
    version?: string
    device?: string
    loadAddress?: string
    voiceArmed?: boolean
    lastPhrase?: string
    engineResult?: string
    submitOutcome?: string
    target?: string
    screenErrors?: string[]
  }
}

interface ClientsResponse {
  connected: number
  clients: ClientRow[]
}

export interface ClientsPanelProps {
  fetchImpl: typeof fetch
  server?: string
}

const dash = (v: string | undefined) => v || '—'

/** ClientsPanel orders a roll call (POST /api/chat/clients/rollcall), waits a
 *  moment for the pages to answer, then lists the registry. Nothing is fetched
 *  until the button is pressed. */
export function ClientsPanel({ fetchImpl, server = '' }: ClientsPanelProps) {
  const [data, setData] = useState<ClientsResponse | null>(null)
  const [error, setError] = useState('')

  const rollCall = useCallback(async () => {
    setError('')
    try {
      const order = await fetchImpl(`${server}/api/chat/clients/rollcall`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}',
      })
      if (!order.ok) throw new Error(`rollcall: HTTP ${order.status}`)
      await new Promise((r) => setTimeout(r, 1500))
      const res = await fetchImpl(`${server}/api/chat/clients`, { headers: { accept: 'application/json' } })
      if (!res.ok) throw new Error(`clients: HTTP ${res.status}`)
      setData((await res.json()) as ClientsResponse)
    } catch (e) {
      setError(String((e as Error).message ?? e))
    }
  }, [fetchImpl, server])

  return (
    <section aria-label="connected pages">
      <h2>Connected pages</h2>
      <p className="muted">
        Every page announces itself on connect. Roll call orders them all to report now.
      </p>
      <button onClick={() => void rollCall()}>Roll call</button>
      {error && <p className="note warn">{error}</p>}
      {data && (
        <ul data-testid="clients">
          {data.clients.length === 0 && <li className="muted">no page has announced itself</li>}
          {data.clients.map((c) => (
            <li key={c.clientId}>
              <strong>{dash(c.state.device)}</strong> · v{dash(c.state.version)} ·{' '}
              {c.state.voiceArmed === undefined ? 'voice —' : c.state.voiceArmed ? 'voice armed' : 'voice off'} ·{' '}
              {c.stale ? 'STALE' : 'live'} · {dash(c.state.loadAddress)} · heard “{dash(c.state.lastPhrase)}” →{' '}
              {dash(c.state.engineResult)} · submit {dash(c.state.submitOutcome)} · target {dash(c.state.target)}
              {c.state.screenErrors?.length ? ` · errors: ${c.state.screenErrors.join(' | ')}` : ''}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
