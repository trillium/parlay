import { randomUUID } from "crypto"
import type { ChatMessage } from "./types"
import { pushToHistory, persistMessage } from "./storage"
import { agents, pollWaiters, setAgentPresence, broadcastToClients } from "./sse"

// ── Message creation ─────────────────────────────────────────────────────────

// Resolve all matching poll waiters for an alert message (not just one)
function resolveWaiters(msg: ChatMessage) {
  let delivered = 0
  const target = msg.channel
  for (let i = pollWaiters.length - 1; i >= 0; i--) {
    const w = pollWaiters[i]
    if (target ? w.channel === target : !w.channel) {
      pollWaiters.splice(i, 1)
      clearTimeout(w.timer)
      w.resolve(msg)
      delivered++
    }
  }
  if (pollWaiters.length === 0) setAgentPresence(false)
  return delivered
}

// Send an alert to specific agent channels (or all registered agents if none specified).
// Creates one message per channel so each agent receives it on poll.
export function broadcastAlert(text: string, targetAgentIds?: string[]): { channels: number; delivered: number } {
  const channels: (string | undefined)[] = targetAgentIds?.length
    ? targetAgentIds
    : [undefined, ...agents.keys()]   // undefined = global pollers + each named agent

  const now = new Date().toISOString()
  let delivered = 0

  for (const channel of channels) {
    const msg: ChatMessage = {
      id:   randomUUID(),
      role: "user",
      type: "alert",
      ts:   now,
      text,
      ...(channel ? { channel } : {}),
    }
    pushToHistory(msg)
    persistMessage(msg)
    broadcastToClients("message", msg)
    delivered += resolveWaiters(msg)
  }

  return { channels: channels.length, delivered }
}

export function addMessage(role: "user" | "agent", text: string, channel?: string, extra?: Partial<ChatMessage>): ChatMessage {
  const msg: ChatMessage = {
    id:   randomUUID(),
    role,
    ts:   new Date().toISOString(),
    text,
    ...(channel ? { channel } : {}),
    ...(extra ?? {}),
  }
  // A fresh user message starts "queued" — no agent has polled it yet. Set before
  // the broadcast so the SSE echo carries the initial state (no client race).
  if (role === "user") msg.received = false
  pushToHistory(msg)
  persistMessage(msg)
  broadcastToClients("message", msg)
  if (role === "user") {
    // Route to channel-specific waiter first, then fall back to global (no channel) waiters
    const idx = pollWaiters.findIndex(w => msg.channel ? w.channel === msg.channel : !w.channel)
    if (idx !== -1) {
      const [waiter] = pollWaiters.splice(idx, 1)
      clearTimeout(waiter.timer)
      waiter.resolve(msg)
      // The agent had a waiter parked → it receives this immediately. Flip the
      // delivery status so the panel shows queued → received.
      markReceived(msg)
      if (pollWaiters.length === 0) setAgentPresence(false)
    }
  }
  return msg
}

// Mark a user message as delivered to its agent and tell the panel. Idempotent.
export function markReceived(msg: ChatMessage): void {
  if (msg.role !== "user" || msg.received === true) return
  msg.received = true
  broadcastToClients("message_received", { id: msg.id, ...(msg.channel ? { channel: msg.channel } : {}) })
}
