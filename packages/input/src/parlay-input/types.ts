/**
 * Public protocol types + constants for parlay-input. See the package README
 * for the full protocol writeup and the reference implementation in
 * `packages/client/src` (`input.ts`, `sse.ts`, `commands/dispatcher/*`).
 */

/** Protocol major version echoed on every envelope; mismatch ⇒ rejected. */
export const PROTOCOL_V = 1

/**
 * Declared max action age (ms). NOTE: like the reference dispatcher
 * (`packages/client/src/commands/dispatcher/apply.ts`), this is declared but
 * NOT enforced — `applyEnvelope` performs no action-age expiry. Kept for
 * parity with the wire contract's `rejected-expired` result; do not treat its
 * presence as evidence the check runs.
 */
export const ACTION_TTL_MS = 1500

/** One action the server pushes back for this input. */
export interface ParlayAction {
  /** e.g. "setText", "clear", "submitNow", or a host-specific UI verb. */
  verb: string
  args?: {
    text?: string
    /** submitNow: the buffer tail that must still be present to fire. */
    requireTail?: string
    [key: string]: unknown
  }
}

/** The `input_action` SSE payload. Mirrors the reference `ActionEnvelope`. */
export interface ActionEnvelope {
  /** Protocol major version. */
  v: number
  streamId: string
  /** Strictly increasing per stream; a gap ⇒ a dropped SSE event ⇒ resync. */
  seq: number
  /** Echoes the `version` this action was computed against. */
  baseVersion: number
  actions: ParlayAction[]
  timing?: {
    engineEvalNs?: number
    relayMs?: number
    serverOwnedFire?: boolean
  }
}

export type ApplyResult =
  | 'applied'
  | 'rejected-stale'      // echoed baseVersion older than the current buffer version
  | 'rejected-expired'    // declared for wire parity; NOT enforced (see ACTION_TTL_MS)
  | 'rejected-protocol'   // unknown major version
  | 'resync'              // seq gap — a resync was requested

/** Live tab set, used by the server for `{agent}` resolution. */
export interface Tab {
  id: string
  name: string
  nicknames: string[]
}

/** Tears down the DOM listener and any owned SSE connection. Idempotent. */
export type Unsubscribe = () => void

/** Handed to `onAction` for verbs this wrapper does not handle itself. */
export interface ActionContext {
  /** The wired element. */
  element: Element
  /** Read the element's current value/textContent. */
  readValue: () => string
  /** Overwrite the element's value/textContent. */
  writeValue: (text: string) => void
  /** Submit `text` as a message (POST /api/chat/send, or `onSubmit`). */
  submit: (text: string) => void
  device: string
  streamId: string
}

export interface ParlayInputOptions {
  /** Base URL of the parlay server, e.g. "http://localhost:4242". */
  server: string
  /** DOM event to listen for on the element. Defaults to "input". */
  event?: string
  /**
   * Voice-settle debounce in ms: rapid edits collapse into ONE eval of the
   * stabilized text. Defaults to 450 (the reference UI default).
   */
  settleMs?: number
  /** Sent to the server so the engine can gate voice-only behavior. Default false. */
  voiceEnabled?: boolean
  /** Protocol major version to send/expect. Defaults to {@link PROTOCOL_V}. */
  protocolVersion?: number
  /** Reported to the server as `paVersion`. Defaults to the library version. */
  paVersion?: string
  /**
   * Stable per-browser device id (scopes the SSE subscription + every eval).
   * Auto-generated and persisted to localStorage when omitted.
   */
  device?: string
  /** Per-page-load stream id. Defaults to `eval-${device}-${epoch}`. */
  streamId?: string
  /** Live tab set for server-side `{agent}` resolution. Defaults to `() => []`. */
  tabs?: () => Tab[]
  /**
   * Handle verbs this wrapper does not apply itself (pickers, tab switches,
   * navigation, …). The core input verbs (`noop`/`setText`/`clear`/`submitNow`)
   * are always applied to the element directly and never reach here.
   */
  onAction?: (action: ParlayAction, ctx: ActionContext) => void
  /**
   * How `submitNow` actually submits. Defaults to `POST /api/chat/send`.
   * Evaluation never submits on its own — only an explicit `submitNow` action
   * (or a host calling `ctx.submit`) does.
   */
  onSubmit?: (text: string) => void | Promise<void>
  /** Observability hook: called with every envelope's terminal ApplyResult. */
  onApply?: (result: ApplyResult, env: ActionEnvelope) => void
  /** Called on any swallowed network/parse error. */
  onError?: (err: unknown) => void
  /**
   * Plug into an existing shared SSE connection instead of opening one. Given
   * the event name and a handler, return an unsubscribe. When omitted, this
   * wrapper opens its own `EventSource` to `/api/chat/events`.
   */
  subscribe?: (event: string, handler: (data: ActionEnvelope) => void) => Unsubscribe
  /** Backoff bounds for the owned SSE reconnect. Defaults 1000ms → 30000ms. */
  reconnect?: { initialMs?: number; maxMs?: number }
  /** `fetch` implementation. Defaults to the global. */
  fetch?: typeof fetch
  /** `EventSource` constructor for the owned SSE path. Defaults to the global. */
  EventSource?: typeof EventSource
}
