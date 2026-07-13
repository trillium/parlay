import type { SSEClient, PollWaiter, AgentInfo, ChatMessage } from "./types"

// ── SSE state ───────────────────────────────────────────────────────────────

export const sseClients  = new Map<string, SSEClient>()
export const pollWaiters: PollWaiter[] = []
export const agents      = new Map<string, AgentInfo>()

// ── Agent presence ──────────────────────────────────────────────────────────
// True when at least one long-poll waiter is connected.
export let agentActive = false

export function setAgentPresence(active: boolean) {
  if (active === agentActive) return
  agentActive = active
  broadcastToClients("agent_presence", { active })
}

// ── CORS headers ────────────────────────────────────────────────────────────

export const CORS = {
  "Access-Control-Allow-Origin":  "*",
  "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
  "Access-Control-Allow-Headers": "Content-Type",
}

// ── SSE helpers ─────────────────────────────────────────────────────────────

export function sseEvent(event: string, data: unknown): string {
  return `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`
}

export function broadcastToClients(event: string, data: unknown) {
  const payload = sseEvent(event, data)
  for (const client of sseClients.values()) {
    try { client.controller.enqueue(new TextEncoder().encode(payload)) } catch { /* client closed */ }
  }
}
