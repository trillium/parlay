/**
 * @parlay/input — a thin, framework-agnostic DOM input wrapper for the
 * parlay protocol. Wires any DOM element (input, textarea, contenteditable,
 * ...) to a parlay server: outbound value changes are sent to the server,
 * and the server can push `action` callbacks back to the caller.
 *
 * NOTE: the transport below (WebSocket/POST to `/api/input`) does not match
 * parlay's real server protocol — that endpoint doesn't exist. The real
 * system is REST (`POST /api/chat/eval`) + a single shared SSE stream
 * (`GET /api/chat/events`) with a versioned staleness/resync contract. See
 * README.md for the full protocol and what a real implementation needs.
 */

/** An action pushed from the parlay server back to the caller. */
export interface ParlayAction {
  /** Discriminator for the action payload, e.g. "clear", "focus", "set-value". */
  type: string
  /** Action-specific data. */
  payload?: unknown
}

export type ParlayTransport = 'websocket' | 'post'

export interface ParlayInputOptions {
  /**
   * Base URL of the parlay server, e.g. "http://localhost:4242".
   * For the "websocket" transport this is translated to a ws:// / wss://
   * URL automatically.
   */
  server: string
  /** DOM event to listen for on `element`. Defaults to "input". */
  event?: string
  /**
   * Called whenever the server pushes an action back to this input.
   * Only invoked while the "websocket" transport is connected — see the
   * README for the current state of server -> client delivery on "post".
   */
  action?: (action: ParlayAction) => void
  /**
   * Transport used to send outbound value updates.
   * "websocket" (default) opens a single duplex connection used for both
   * sending values and receiving actions. "post" sends each value with a
   * plain HTTP request and is a partial skeleton — see README/TODOs below.
   */
  transport?: ParlayTransport
  /** Path appended to `server` for the chosen transport. Defaults to "/api/input". */
  path?: string
}

/** Tears down the DOM listener and any open server connection. */
export type Unsubscribe = () => void

function readValue(element: Element): string {
  if ('value' in element) return String((element as HTMLInputElement).value ?? '')
  return element.textContent ?? ''
}

function toWebSocketUrl(server: string, path: string): string {
  const url = new URL(path, server)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}

/**
 * Attach `element` to a parlay server: forwards `event` on the element to
 * the server, and (in "websocket" mode) delivers server-pushed actions to
 * `options.action`.
 *
 * @returns a function that removes the DOM listener and closes any open
 * connection. Safe to call multiple times.
 */
export function parlayInput(element: Element, options: ParlayInputOptions): Unsubscribe {
  const {
    server,
    event = 'input',
    action,
    transport = 'websocket',
    path = '/api/input',
  } = options

  let socket: WebSocket | null = null
  let closed = false

  function handleServerMessage(evt: MessageEvent) {
    if (!action) return
    let parsed: unknown
    try {
      parsed = JSON.parse(evt.data)
    } catch {
      return // ignore malformed frames
    }
    if (parsed && typeof parsed === 'object' && 'type' in parsed) {
      action(parsed as ParlayAction)
    }
  }

  function ensureSocket(): WebSocket | null {
    if (closed) return null
    if (socket) return socket
    socket = new WebSocket(toWebSocketUrl(server, path))
    socket.addEventListener('message', handleServerMessage)
    return socket
  }

  function sendViaWebSocket(value: string) {
    const ws = ensureSocket()
    if (!ws) return
    const send = () => ws.send(JSON.stringify({ type: 'input', event, value }))
    if (ws.readyState === WebSocket.OPEN) {
      send()
    } else if (ws.readyState === WebSocket.CONNECTING) {
      // TODO: for high-frequency events (e.g. "input" on every keystroke),
      // coalesce to the latest value instead of queuing one listener per event.
      ws.addEventListener('open', send, { once: true })
    }
    // CLOSING/CLOSED: drop silently: a reconnect strategy belongs here once
    // the server-side reconnect/backoff contract is settled.
  }

  function sendViaPost(value: string) {
    // TODO: finalize the request/response shape against the real parlay
    // server endpoint (auth headers, batching, error surface).
    const url = new URL(path, server)
    fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ event, value }),
    }).catch(() => {})
    // TODO: "post" is send-only today. Delivering server -> client `action`
    // callbacks without a socket needs a companion channel (SSE or polling)
    // — not yet implemented, so `options.action` is not invoked in this mode.
  }

  function handleDomEvent() {
    const value = readValue(element)
    if (transport === 'websocket') sendViaWebSocket(value)
    else sendViaPost(value)
  }

  element.addEventListener(event, handleDomEvent)
  if (transport === 'websocket') ensureSocket()

  return function unsubscribe() {
    if (closed) return
    closed = true
    element.removeEventListener(event, handleDomEvent)
    if (socket) {
      socket.removeEventListener('message', handleServerMessage)
      socket.close()
      socket = null
    }
  }
}
