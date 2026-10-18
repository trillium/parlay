/**
 * The owned SSE down-channel: opens an `EventSource` to `/api/chat/events`,
 * parses `input_action` envelopes, and reconnects with exponential backoff,
 * resuming from the last seen `message` id so reconnects replay the delta.
 * Only used when the host does not supply its own shared `subscribe`.
 */
import type { ActionEnvelope, Unsubscribe } from './types'

function pageUrl(): string {
  try { return (globalThis as { location?: Location }).location?.href ?? '' } catch { return '' }
}

export interface OwnedSseConfig {
  base: string
  device: string
  EventSourceImpl?: typeof EventSource
  reconnect?: { initialMs?: number; maxMs?: number }
  onEnvelope: (env: ActionEnvelope) => void
  onError?: (err: unknown) => void
}

export function openOwnedSse(cfg: OwnedSseConfig): Unsubscribe {
  const { base, device, EventSourceImpl, reconnect, onEnvelope, onError } = cfg
  if (!EventSourceImpl) return () => {}
  const initialMs = reconnect?.initialMs ?? 1000
  const maxMs = reconnect?.maxMs ?? 30_000
  let delay = initialMs
  let es: EventSource | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let stopped = false
  // Last message id seen on this stream. Reconnects resume with ?after= so
  // a flapping stream replays the delta, not the full windowed history —
  // the same resume contract the panel client uses (docs/api-contract.md
  // "SSE Events"). Only `message` frames carry ids; input_action
  // envelopes do not, so they never advance the cursor.
  let lastMessageId = ''

  const connect = () => {
    if (stopped) return
    let url =
      `${base}/api/chat/events?device=${encodeURIComponent(device)}` +
      `&url=${encodeURIComponent(pageUrl())}`
    if (lastMessageId) url += `&after=${encodeURIComponent(lastMessageId)}`
    const thisEs: EventSource = new EventSourceImpl(url)
    es = thisEs
    // Reset backoff once a connection is actually established.
    thisEs.addEventListener('open', () => { if (es === thisEs) delay = initialMs })
    thisEs.addEventListener('message', (e: MessageEvent) => {
      if (es !== thisEs) return
      try {
        const id = (JSON.parse((e as MessageEvent).data) as { id?: unknown }).id
        if (typeof id === 'string' && id) lastMessageId = id
      } catch { /* malformed frame: cursor stays where it was */ }
    })
    thisEs.addEventListener('input_action', (e: MessageEvent) => {
      if (es !== thisEs) return
      try { onEnvelope(JSON.parse(e.data)) } catch (err) { onError?.(err) }
    })
    // This handler closes ITS OWN connection, never the module-level `es`.
    // A handler bound to `es` closes whatever is current, so one late `error`
    // from a superseded instance tears down the live stream that replaced it.
    // Two guards: (1) only the live instance may act, and (2) at most ONE
    // reconnect timer is ever outstanding — a second error before the first
    // timer fires would otherwise stack a second connection.
    thisEs.onerror = () => {
      if (es !== thisEs) return
      try { thisEs.close() } catch { /* already closed */ }
      if (stopped) return
      if (reconnectTimer != null) return
      reconnectTimer = setTimeout(() => { reconnectTimer = null; connect() }, delay)
      delay = Math.min(delay * 2, maxMs)
    }
  }
  connect()

  return () => {
    stopped = true
    if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null }
    // Drop the reference BEFORE closing, so any handler still queued on the
    // instance fails its `es !== thisEs` liveness check and does nothing.
    const last = es
    es = null
    try { last?.close() } catch { /* already closed */ }
  }
}
