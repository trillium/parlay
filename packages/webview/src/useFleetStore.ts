import { useState, useEffect, useCallback, useRef } from 'react'
import { fetchAgents, fetchCommands, fetchHistory, fetchSubscribers, openEventStream } from './api'
import type { Agent, Command, Message, ToolEvent } from './types'

const MAX_EVENTS = 500
const MAX_MESSAGES = 200

export interface FleetStore {
  agents: Agent[]
  liveChannels: Set<string>
  commands: Command[]
  messages: Message[]           // thread for selected channel (or recent fleet msgs)
  toolEvents: ToolEvent[]
  selectedChannel: string | null
  selectChannel: (id: string | null) => void
  connectedClients: number
}

export function useFleetStore(): FleetStore {
  const [agents, setAgents] = useState<Agent[]>([])
  const [liveChannels, setLiveChannels] = useState<Set<string>>(new Set())
  const [commands, setCommands] = useState<Command[]>([])
  const [messages, setMessages] = useState<Message[]>([])
  const [toolEvents, setToolEvents] = useState<ToolEvent[]>([])
  const [selectedChannel, setSelectedChannel] = useState<string | null>(null)
  const [connectedClients, setConnectedClients] = useState(0)
  const selectedRef = useRef<string | null>(null)

  const selectChannel = useCallback(async (id: string | null) => {
    selectedRef.current = id
    setSelectedChannel(id)
    const msgs = await fetchHistory(id ?? undefined, MAX_MESSAGES)
    if (selectedRef.current === id) setMessages(msgs)
  }, [])

  useEffect(() => {
    // initial load
    fetchAgents().then(setAgents)
    fetchCommands().then(setCommands)
    fetchSubscribers().then(({ clients, channels }) => {
      setConnectedClients(clients)
      setLiveChannels(new Set(channels.map(c => c.channel)))
    })
    fetchHistory(undefined, 50).then(setMessages)

    const stop = openEventStream((type, data: any) => {
      if (type === 'message') {
        const msg = data as Message
        setMessages(prev => {
          if (selectedRef.current && msg.channel !== selectedRef.current) return prev
          const next = [...prev, msg]
          return next.length > MAX_MESSAGES ? next.slice(-MAX_MESSAGES) : next
        })
      }
      if (type === 'tool_event') {
        setToolEvents(prev => {
          const next = [...prev, data as ToolEvent]
          return next.length > MAX_EVENTS ? next.slice(-MAX_EVENTS) : next
        })
      }
      if (type === 'agent_register') {
        const a = data as Agent
        setAgents(prev => {
          const exists = prev.find(x => x.id === a.id)
          return exists ? prev : [...prev, a]
        })
      }
      if (type === 'commands') {
        setCommands((data as any).commands ?? [])
      }
      if (type === 'command_update') {
        const cmd = data as Command
        setCommands(prev => {
          const idx = prev.findIndex(c => c.id === cmd.id)
          if (idx === -1) return [...prev, cmd]
          const next = [...prev]
          next[idx] = cmd
          return next
        })
      }
      if (type === 'presence_map') {
        const map = data as Record<string, any>
        setLiveChannels(new Set(Object.keys(map)))
      }
      if (type === 'agents') {
        if (Array.isArray(data)) setAgents(data)
      }
    })
    return stop
  }, [])

  return { agents, liveChannels, commands, messages, toolEvents, selectedChannel, selectChannel, connectedClients }
}
