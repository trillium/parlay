import { history, historyIndex } from "./storage"
import { agents, pollWaiters, setAgentPresence, CORS, broadcastToClients, lastPollByChannel, broadcastPresenceMap, persistAgents } from "./sse"
import { markReceived } from "./messages"
import type { PollWaiter } from "./types"

export function handlePollRequest(req: Request, pathname: string): Response | null {
  if (req.method !== "GET" || pathname !== "/api/chat/poll") return null

  const params  = new URL(req.url).searchParams
  const afterId = params.get("after") ?? ""
  const channel = params.get("channel") ?? undefined
  if (channel) {
    lastPollByChannel.set(channel, Date.now())
    if (!agents.has(channel)) {
      const info = { id: channel, name: channel, color: "#6b7280" }
      agents.set(channel, info)
      broadcastToClients("agent_register", info)
      persistAgents()
    }
    broadcastPresenceMap()
  }
  const afterIdx = afterId ? (historyIndex.get(afterId) ?? -1) : -1
  const pending  = history.slice(afterIdx + 1).filter(m =>
    m.role === "user" && (channel ? m.channel === channel : !m.channel)
  )
  if (pending.length > 0) {
    markReceived(pending[0])
    return new Response(JSON.stringify(pending[0]), {
      headers: { "Content-Type": "application/json", ...CORS },
    })
  }
  let waiter: PollWaiter | null = null
  const removeWaiter = () => {
    if (!waiter) return
    const idx = pollWaiters.indexOf(waiter)
    if (idx !== -1) pollWaiters.splice(idx, 1)
    if (pollWaiters.length === 0) setAgentPresence(false)
  }
  return new Response(new ReadableStream({
    start(controller) {
      const enc = new TextEncoder()
      const timer = setTimeout(() => {
        removeWaiter()
        broadcastPresenceMap()
        try { controller.enqueue(enc.encode(JSON.stringify({ timeout: true }))); controller.close() } catch { /* client gone */ }
      }, 30_000)
      waiter = {
        resolve(msg) {
          try { controller.enqueue(enc.encode(JSON.stringify(msg))); controller.close() } catch { /* client gone */ }
        },
        timer,
        channel,
      }
      pollWaiters.push(waiter)
      setAgentPresence(true)
    },
    cancel() {
      if (waiter) clearTimeout(waiter.timer)
      removeWaiter()
      broadcastPresenceMap()
    },
  }), { headers: { "Content-Type": "application/json", ...CORS } })
}
