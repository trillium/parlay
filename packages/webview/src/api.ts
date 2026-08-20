import type { Agent, Command, Message, Subscriber } from './types'

const BASE = '/api/chat'

export async function fetchAgents(): Promise<Agent[]> {
  const r = await fetch(`${BASE}/agents`)
  if (!r.ok) return []
  const agents = await r.json()
  return Array.isArray(agents) ? agents : []
}

export async function fetchHistory(channel?: string, limit = 100): Promise<Message[]> {
  const params = new URLSearchParams({ limit: String(limit) })
  if (channel) params.set('channel', channel)
  const r = await fetch(`${BASE}/history?${params}`)
  if (!r.ok) return []
  const data = await r.json()
  return Array.isArray(data) ? data : data.messages ?? []
}

export async function fetchCommands(): Promise<Command[]> {
  const r = await fetch(`${BASE}/commands`)
  if (!r.ok) return []
  const data = await r.json()
  return data.commands ?? []
}

export async function fetchSubscribers(): Promise<{ clients: number; channels: Subscriber[] }> {
  const r = await fetch(`${BASE}/subscribers`)
  if (!r.ok) return { clients: 0, channels: [] }
  const data = await r.json()
  const poll = data.poll ?? {}
  return {
    clients: data.parlay?.clients ?? 0,
    channels: poll.channels ?? [],
  }
}

export function openEventStream(onEvent: (type: string, data: unknown) => void): () => void {
  const es = new EventSource(`${BASE}/events`)
  const names = ['message', 'tool_event', 'agent_register', 'commands', 'command_update', 'presence_map', 'connected', 'history', 'agents']
  const handlers: [string, (e: MessageEvent) => void][] = names.map(name => {
    const h = (e: MessageEvent) => {
      try { onEvent(name, JSON.parse(e.data)) } catch { /* skip malformed */ }
    }
    es.addEventListener(name, h)
    return [name, h]
  })
  return () => {
    handlers.forEach(([name, h]) => es.removeEventListener(name, h))
    es.close()
  }
}
