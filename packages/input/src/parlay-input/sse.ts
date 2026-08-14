/**
 * The owned SSE down-channel: opens an `EventSource` to `/api/chat/events`,
 * parses `input_action` envelopes, and reconnects with exponential backoff.
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

  const connect = () => {
    if (stopped) return
    const url =
      `${base}/api/chat/events?device=${encodeURIComponent(device)}` +
      `&url=${encodeURIComponent(pageUrl())}`
    es = new EventSourceImpl(url)
    // Reset backoff once a connection is actually established.
    es.addEventListener('open', () => { delay = initialMs })
    es.addEventListener('input_action', (e: MessageEvent) => {
      try { onEnvelope(JSON.parse(e.data)) } catch (err) { onError?.(err) }
    })
    es.onerror = () => {
      try { es?.close() } catch { /* already closed */ }
      if (stopped) return
      reconnectTimer = setTimeout(connect, delay)
      delay = Math.min(delay * 2, maxMs)
    }
  }
  connect()

  return () => {
    stopped = true
    if (reconnectTimer) clearTimeout(reconnectTimer)
    try { es?.close() } catch { /* already closed */ }
  }
}
