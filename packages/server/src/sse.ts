import type { SSEClient, PollWaiter, AgentInfo, ChatMessage } from "./types"
import { shouldDeliver, countSuppressed } from "./capability"
import { rewriteMessageForServe } from "./link-rewrite"

// ── SSE state ───────────────────────────────────────────────────────────────

export const sseClients  = new Map<string, SSEClient>()
export const pollWaiters: PollWaiter[] = []
export const agents      = new Map<string, AgentInfo>()

// ── Agent registry persistence (#18) ────────────────────────────────────────
// The agents map is identity only — a Pulse restart used to wipe it and every
// tab vanished until each agent happened to post again. Write-through to disk
// on every registration; rehydrate at module init. Presence/liveness stays
// computed from live pollers as before.
import { readFileSync, writeFileSync, mkdirSync } from "fs"
import { join } from "path"
import { AGENTS_FILE as AGENTS_PATH } from "./paths"

export function persistAgents(): void {
  try {
    mkdirSync(join(AGENTS_PATH, ".."), { recursive: true })
    writeFileSync(AGENTS_PATH, JSON.stringify(Array.from(agents.values()), null, 2) + "\n", "utf-8")
  } catch { /* best-effort */ }
}

try {
  const list = JSON.parse(readFileSync(AGENTS_PATH, "utf-8")) as AgentInfo[]
  for (const a of list) {
    if (a?.id) agents.set(String(a.id), {
      id:    String(a.id),
      name:  String(a.name  ?? a.id),
      color: String(a.color ?? "#6b7280"),
      ...(Array.isArray(a.nicknames) && a.nicknames.length ? { nicknames: a.nicknames.map(String) } : {}),
      ...(Array.isArray(a.urls)      && a.urls.length      ? { urls:      a.urls.map(String)      } : {}),
      ...(Array.isArray(a.path)      && a.path.length      ? { path:      a.path.map(String)      } : {}),
    })
  }
} catch { /* first boot or unreadable — start empty */ }

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
// The wildcard CORS constant now lives in guard/paths.ts, next to the route
// set that decides which routes still get it: it applies to the READ/SSE
// routes only, while the guarded routes are re-headered with a reflected
// single origin (guard/index.ts's withGuardedCors). Re-exported here so
// existing importers keep their `from "./sse"` path.
export { CORS } from "./guard"

// ── SSE helpers ─────────────────────────────────────────────────────────────

export function sseEvent(event: string, data: unknown): string {
  return `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`
}

// Both broadcast paths run the capability delivery gate
// (docs/interface-capabilities.md): a client that declared ?caps= only
// receives the gated presentation commands it accepts; undeclared clients are
// legacy and get everything, byte-identical. Suppressions are counted — a
// silent gate is indistinguishable from a gate that never runs.
// Returns how many clients the event was delivered to — a suppressed client
// does not count, so callers report delivery truth, not addressing truth.
export function broadcastToClients(event: string, data: unknown): number {
  // "message" carries a ChatMessage whose text may hold localhost links —
  // rewrite the served view only, never the stored object (see link-rewrite.ts).
  const servedData = event === "message" ? rewriteMessageForServe(data as ChatMessage) : data
  const payload = sseEvent(event, servedData)
  let delivered = 0
  for (const client of sseClients.values()) {
    if (!shouldDeliver(client.caps, event)) { countSuppressed(event); continue }
    delivered++
    try { client.controller.enqueue(new TextEncoder().encode(payload)) } catch { /* client closed */ }
  }
  return delivered
}

// Scoped variant: deliver only to SSE clients that registered the given device id.
// Returns how many clients the command was delivered to — a suppressed client
// does not count, so callers report delivery truth, not addressing truth.
export function broadcastToDevice(deviceId: string, event: string, data: unknown): number {
  const payload = sseEvent(event, data)
  let matched = 0
  for (const client of sseClients.values()) {
    if (client.device !== deviceId) continue
    if (!shouldDeliver(client.caps, event)) { countSuppressed(event); continue }
    matched++
    try { client.controller.enqueue(new TextEncoder().encode(payload)) } catch { /* client closed */ }
  }
  return matched
}
