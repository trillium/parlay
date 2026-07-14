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

// ── Per-channel presence ────────────────────────────────────────────────────
// A channel is "listening" if it long-polled within the window. pollWaiters are
// transient (30s max), so presence is tracked by last-poll timestamp, not by
// current waiter membership. Registered-but-never-polled channels are omitted
// from the map — the client renders those as offline.
export const LISTEN_WINDOW_MS = 35_000
export const lastPollByChannel = new Map<string, number>()

export function computePresenceMap(): Record<string, "listening" | "idle"> {
  const now = Date.now()
  const map: Record<string, "listening" | "idle"> = {}
  for (const [ch, last] of lastPollByChannel) {
    map[ch] = now - last < LISTEN_WINDOW_MS ? "listening" : "idle"
  }
  return map
}

// Broadcast a presence_map SSE event when the map actually changed (or forced).
// presenceBroadcasts counts actual emissions — exposed on /api/chat/subscribers
// so a flat counter proves polls without state transitions stay silent.
export let presenceBroadcasts = 0
let _lastPresenceJson = ""
export function broadcastPresenceMap(force = false) {
  const map = computePresenceMap()
  const json = JSON.stringify(map)
  if (!force && json === _lastPresenceJson) return
  _lastPresenceJson = json
  presenceBroadcasts++
  broadcastToClients("presence_map", map)
}

// Sweep: a channel whose monitor died stops polling, so no event would ever
// flip it to idle — recompute periodically and broadcast only on change.
setInterval(() => broadcastPresenceMap(), 10_000)

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
