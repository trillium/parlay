import { afterEach, describe, expect, test } from 'bun:test'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { ClientsPanel } from './ClientsPanel'

let root: Root
afterEach(async () => { await act(async () => root.unmount()) })

describe('ClientsPanel', () => {
  test('roll call posts the order then lists the registry, absent fields as a dash', async () => {
    const calls: string[] = []
    const fetchImpl = (async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.endsWith('/rollcall')) return new Response('{"id":"rc-1","connected":1}')
      return new Response(JSON.stringify({
        connected: 1,
        clients: [{ clientId: 'c1', stale: false, lastSeen: 't', state: { device: 'phone', version: '5.13.0', voiceArmed: true, screenErrors: ['offline'] } }],
      }))
    }) as unknown as typeof fetch
    const host = document.createElement('div')
    document.body.appendChild(host)
    root = createRoot(host)
    await act(async () => root.render(<ClientsPanel fetchImpl={fetchImpl} />))
    expect(calls).toEqual([])
    await act(async () => { host.querySelector('button')!.click(); await new Promise((r) => setTimeout(r, 1700)) })
    expect(calls).toEqual(['POST /api/chat/clients/rollcall', 'GET /api/chat/clients'])
    const text = host.querySelector('[data-testid=clients]')!.textContent!
    expect(text).toContain('phone')
    expect(text).toContain('voice armed')
    expect(text).toContain('submit —')
    expect(text).toContain('errors: offline')
  })
})
