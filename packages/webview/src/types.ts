export interface Agent {
  id: string
  name: string
  color: string
}

export interface Message {
  id: string
  role: string
  ts: string
  text: string
  channel?: string
  type?: string
  source?: string
  from?: string
  meta?: Record<string, unknown>
}

export interface Command {
  id: string
  verb: string
  flags?: string[]
  agent?: string
  pid?: number
  state: string
  startedAt: string
  updatedAt: string
  durationMs: number
  outcome?: string
}

export interface ToolEvent {
  tool: string
  desc?: string
  cmd?: string
  out?: string
  err?: string
  ts: string
  channel?: string
}

export interface Subscriber {
  channel: string
  id?: string
  name?: string
  lastSeen?: string
}

export interface PresenceEntry {
  channel: string
  lastSeen?: string
  status?: string
}

export interface EvalAction {
  verb: string
  args?: {
    text?: string
    channel?: string
    url?: string
    reason?: string
    timerId?: string
    fireInMs?: number
    hintId?: string
    hintKind?: string
    [key: string]: unknown
  }
}

export interface EvalHit {
  ts: string
  streamId: string
  seq: number
  device?: string
  actions: EvalAction[]
  timing?: { engineEvalNs?: number; relayMs?: number; serverOwnedFire?: boolean }
  serverOwned?: boolean
}
