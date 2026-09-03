import { history, historyIndex } from "./storage"
import { pollWaiters, setAgentPresence, CORS, lastPollByChannel, broadcastPresenceMap } from "./sse"
import { markReceived } from "./messages"
import { isTombstoned } from "./prune"
import { rewriteMessageForServe } from "./link-rewrite"
import type { PollWaiter } from "./types"

// task-1t0m: GET /api/chat/poll must be genuinely read-only, not read-only-
// by-accident-of-the-origin-guard. It used to auto-register any unrecognized
// channel (agents.set + persistAgents — a disk write from a plain GET) so a
// leaked listener could resurrect its own pruned registry row on its very
// next poll (robots-ycfa's 82-orphan incident). Registration now happens
// only through the explicit, already-guarded POST /api/chat/register-agent
// — every real caller (parlay listen, parlay monitor, parlay claim, parlay
// nickname) already calls it before polling. Presence bookkeeping
// (lastPollByChannel/broadcastPresenceMap below) stays: it is in-memory only,
// never persisted, and reflects the read itself rather than mutating any
// identity/message record.
export function handlePollRequest(req: Request, pathname: string): Response | null {
  if (req.method !== "GET" || pathname !== "/api/chat/poll") return null

  const params  = new URL(req.url).searchParams
  const afterId = params.get("after") ?? ""
  const channel = params.get("channel") ?? undefined
  // A channel that was deliberately removed (prune sweep or explicit
  // unregister) must not be able to re-create itself by polling. Answer 410
  // Gone — a terminal status the relay treats as "stop polling this channel".
  // Deliberately BEFORE the lastPoll bookkeeping: a refused poll is not presence.
  if (channel && isTombstoned(channel)) {
    return new Response(JSON.stringify({
      error: `channel ${channel} was unregistered; stop polling`,
      gone: true,
    }), { status: 410, headers: { "Content-Type": "application/json", ...CORS } })
  }
  if (channel) {
    lastPollByChannel.set(channel, Date.now())
    broadcastPresenceMap()
  }
  const afterIdx = afterId ? (historyIndex.get(afterId) ?? -1) : -1
  const pending  = history.slice(afterIdx + 1).filter(m =>
    m.role === "user" && (channel ? m.channel === channel : !m.channel)
  )
  if (pending.length > 0) {
    markReceived(pending[0])
    return new Response(JSON.stringify(rewriteMessageForServe(pending[0])), {
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
          try { controller.enqueue(enc.encode(JSON.stringify(rewriteMessageForServe(msg)))); controller.close() } catch { /* client gone */ }
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
