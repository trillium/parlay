// Shared Parlay HTTP probe primitives — register / send / poll / doctor-checks
// against ONE door (a server base URL). two-door and two-stack both build on
// these. Every function is fail-explicit: it returns a typed result, never
// throws for an ordinary HTTP failure, so a probe suite can record a FAIL row
// and keep going.

export interface HttpResult<T> {
  ok: boolean
  status: number
  data?: T
  error?: string
  ms: number
}

async function timed<T>(fn: () => Promise<Response>): Promise<HttpResult<T>> {
  const t0 = performance.now()
  try {
    const res = await fn()
    const ms = performance.now() - t0
    let data: T | undefined
    const text = await res.text()
    if (text) {
      try {
        data = JSON.parse(text) as T
      } catch {
        // A non-JSON body on an otherwise-ok response is itself a failure signal.
        return { ok: false, status: res.status, error: `non-JSON body: ${text.slice(0, 120)}`, ms }
      }
    }
    return { ok: res.ok, status: res.status, data, error: res.ok ? undefined : `${res.status} ${res.statusText}`, ms }
  } catch (err) {
    return { ok: false, status: 0, error: String(err), ms: performance.now() - t0 }
  }
}

export interface AgentInfo {
  id: string
  name?: string
  color?: string
}

/** POST /api/chat/register-agent — register a channel with name/color. */
export function registerAgent(door: string, agent: AgentInfo): Promise<HttpResult<{ ok?: boolean; error?: string }>> {
  return timed(() =>
    fetch(`${door}/api/chat/register-agent`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(agent),
    }),
  )
}

/**
 * POST /api/chat/send — post a user message onto a channel (toAgent). `from`
 * attributes the sender so the message never masquerades as the captain.
 */
export function sendMessage(
  door: string,
  opts: { text: string; toAgent?: string; from?: string },
): Promise<HttpResult<{ ok?: boolean; id?: string; error?: string }>> {
  return timed(() =>
    fetch(`${door}/api/chat/send`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(opts),
    }),
  )
}

export interface PollMessage {
  timeout?: boolean
  id?: string
  role?: string
  text?: string
  ts?: string
  channel?: string
  from?: string
}

/**
 * GET /api/chat/poll — one long-poll cycle. Resolves with the next user message
 * on `channel` after `after`, or {timeout:true} after ~30s. `signal` lets the
 * caller bound or cancel the wait (soak mode holds many of these concurrently).
 */
export function poll(
  door: string,
  opts: { after?: string; channel?: string; signal?: AbortSignal },
): Promise<HttpResult<PollMessage>> {
  const qs = new URLSearchParams()
  if (opts.after) qs.set("after", opts.after)
  if (opts.channel) qs.set("channel", opts.channel)
  return timed(() => fetch(`${door}/api/chat/poll?${qs.toString()}`, { signal: opts.signal }))
}

export interface SubscribersInfo {
  parlay?: { clients?: number }
  poll?: { count?: number }
  registered?: { count?: number; agents?: AgentInfo[] }
  presence?: Array<{ channel: string; status: string; lastSeen: string | null }>
  memory?: Record<string, number>
}

/** GET /api/chat/subscribers — server vitals + presence (doctor-equivalent). */
export function subscribers(door: string): Promise<HttpResult<SubscribersInfo>> {
  return timed(() => fetch(`${door}/api/chat/subscribers`))
}

/** GET /api/chat/agents — the registry. */
export function agentsList(door: string): Promise<HttpResult<AgentInfo[]>> {
  return timed(() => fetch(`${door}/api/chat/agents`))
}

/**
 * Register on channel A then long-poll on channel B until the exact message id
 * we sent arrives (or `timeoutMs` elapses). This is the cross-door delivery
 * assertion: same backing store means a send via one door is visible via another.
 * Returns the id observed + the observed latency, or an error string.
 */
export async function sendViaAObserveViaB(opts: {
  sendDoor: string
  observeDoor: string
  channel: string
  from: string
  text: string
  timeoutMs?: number
}): Promise<{ ok: boolean; sentId?: string; observedMs?: number; sendMs?: number; error?: string }> {
  const timeoutMs = opts.timeoutMs ?? 10_000
  const deadline = Date.now() + timeoutMs

  const sent = await sendMessage(opts.sendDoor, { text: opts.text, toAgent: opts.channel, from: opts.from })
  if (!sent.ok || !sent.data?.id) {
    return { ok: false, error: `send failed: ${sent.error ?? sent.data?.error ?? "no id"}`, sendMs: sent.ms }
  }
  const sentId = sent.data.id

  // Long-poll the observe door for messages on the channel, walking the cursor
  // until we see our id or time out. after starts empty so the very first
  // pending message (ours, if store is fresh for this channel) is returned.
  let after = ""
  const t0 = performance.now()
  while (Date.now() < deadline) {
    const remaining = deadline - Date.now()
    const controller = new AbortController()
    const kill = setTimeout(() => controller.abort(), Math.min(remaining, 31_000))
    const got = await poll(opts.observeDoor, { after, channel: opts.channel, signal: controller.signal })
    clearTimeout(kill)
    if (!got.ok) {
      // Network-level failure on the observe door — abort with the reason.
      return { ok: false, sentId, error: `poll failed: ${got.error}`, sendMs: sent.ms }
    }
    if (got.data?.timeout) continue // no message yet; poll again
    if (got.data?.id) {
      after = got.data.id
      if (got.data.id === sentId) {
        return { ok: true, sentId, observedMs: performance.now() - t0, sendMs: sent.ms }
      }
      // Some other message on the channel; advance the cursor and keep looking.
      continue
    }
    // Malformed poll response with neither timeout nor id.
    return { ok: false, sentId, error: "poll returned neither timeout nor message", sendMs: sent.ms }
  }
  return { ok: false, sentId, error: `id ${sentId} not observed within ${timeoutMs}ms`, sendMs: sent.ms }
}
