/**
 * The `parlayInput` entry point: wires a DOM element to a parlay server.
 *
 *   up:   every edit → debounced `POST /api/chat/eval` carrying the current
 *         buffer + a monotonically-bumped `version` (the staleness token).
 *   down: the server pushes `input_action` envelopes over the shared
 *         `GET /api/chat/events` SSE stream; each envelope is validated
 *         (protocol version, `baseVersion` staleness, `seq` ordering) before
 *         any of its actions touch the element.
 *
 * There is NO client-side evaluation and actions are applied ONLY from the SSE
 * stream — never from the synchronous POST response — so a single ordering
 * source of truth covers both live POSTs and the engine's own server-owned
 * timer fires. This mirrors the reference implementation in
 * `packages/client/src` (`input.ts`, `sse.ts`, `commands/dispatcher/*`).
 */
import {
  PROTOCOL_V,
  type ActionContext,
  type ActionEnvelope,
  type ApplyResult,
  type ParlayAction,
  type ParlayInputOptions,
  type Unsubscribe,
} from './types'
import {
  LIB_VERSION,
  getDeviceId,
  isMutating,
  randomId,
  readCursor,
  readValue,
  writeValue,
} from './dom'
import { openOwnedSse } from './sse'

/**
 * Attach `element` to a parlay server. Returns an idempotent unsubscribe that
 * removes the DOM listener and closes any SSE connection this wrapper owns.
 */
export function parlayInput(element: Element, options: ParlayInputOptions): Unsubscribe {
  const {
    server,
    event = 'input',
    settleMs = 450,
    voiceEnabled = false,
    protocolVersion = PROTOCOL_V,
    paVersion = LIB_VERSION,
    tabs = () => [],
    onAction,
    onSubmit,
    onApply,
    onError,
    subscribe,
    reconnect,
    fetch: fetchImpl = globalThis.fetch,
    EventSource: EventSourceImpl = (globalThis as { EventSource?: typeof EventSource }).EventSource,
  } = options

  const base = server.replace(/\/+$/, '')
  const device = options.device ?? getDeviceId()
  const streamId = options.streamId ?? `eval-${device}-${randomId('e')}`

  // Client-owned monotonic buffer version — the staleness token. Bumped on
  // EVERY local mutation (user edit or resync), never wall-clock, so
  // same-millisecond edits can't collide.
  let version = 0
  let closed = false
  let settleTimer: ReturnType<typeof setTimeout> | null = null
  // Per-stream expected seq (strict ordering). Kept as a map for parity with
  // the reference even though this wrapper drives a single stream.
  const expectedSeq = new Map<string, number>()

  function bumpVersion(): number { return ++version }

  function scheduleEval(immediate: boolean, reason: string): void {
    if (closed) return
    const fire = () => { void postEval(reason) }
    if (immediate) { fire(); return }
    if (settleTimer) clearTimeout(settleTimer)
    settleTimer = setTimeout(fire, Math.max(0, settleMs))
  }

  async function postEval(reason: string): Promise<void> {
    if (!fetchImpl) return
    const sentVersion = version
    try {
      const r = await fetchImpl(`${base}/api/chat/eval`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          streamId,
          version: sentVersion,
          text: readValue(element),
          cursor: readCursor(element),
          reason,
          voiceEnabled,
          tabs: tabs(),
          device,
          paVersion,
        }),
      })
      // The synchronous response is used ONLY for round-trip timing — actions
      // are applied via the SSE `input_action` path so ordering/staleness is
      // uniform whether an action came from a live POST or a server-owned fire.
      void r
    } catch (err) {
      onError?.(err)
    }
  }

  // A resync re-anchors the server's view: bump the version so the POST carries
  // the freshest token, then send immediately (no settle — the text is current).
  function resync(reason: string): void {
    bumpVersion()
    scheduleEval(true, reason)
  }

  function submit(text: string): void {
    if (onSubmit) { void onSubmit(text); return }
    if (!fetchImpl) return
    fetchImpl(`${base}/api/chat/send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text }),
    }).catch(err => onError?.(err))
  }

  const actionCtx: ActionContext = {
    element,
    readValue: () => readValue(element),
    writeValue: (t: string) => writeValue(element, t),
    submit,
    device,
    streamId,
  }

  // applySubmitNow is the irreversibility guard: the server DECIDED to submit,
  // but its decision is ~1 round-trip stale. Re-verify the required tail against
  // the TRULY current buffer before firing — on a slow link the tail has often
  // already moved.
  function applySubmitNow(a: ParlayAction): ApplyResult {
    const requireTail = String(a.args?.requireTail ?? '')
    const val = readValue(element)
    if (requireTail) {
      const idx = val.toLowerCase().lastIndexOf(requireTail.toLowerCase())
      const after = idx === -1 ? '' : val.slice(idx + requireTail.length).trim().replace(/[.!?,;]+/g, '')
      if (idx === -1 || after !== '') return 'rejected-stale'
      const stripped = val.slice(0, idx).trim()
      if (stripped) submit(stripped)
      return 'applied'
    }
    const t = String(a.args?.text ?? '').trim()
    if (t) submit(t)
    return 'applied'
  }

  function applyAction(a: ParlayAction): ApplyResult {
    try {
      switch (a.verb) {
        case 'noop':
          return 'applied'
        case 'setText':
          writeValue(element, String(a.args?.text ?? ''))
          return 'applied'
        case 'clear':
          writeValue(element, '')
          return 'applied'
        case 'submitNow':
          return applySubmitNow(a)
        default:
          // Host-specific / forward-compat verbs: delegate, never wedge.
          onAction?.(a, actionCtx)
          return 'applied'
      }
    } catch (err) {
      onError?.(err)
      return 'applied' // an action must never break input
    }
  }

  function applyEnvelope(env: ActionEnvelope): ApplyResult {
    if (env.v !== protocolVersion) {
      onApply?.('rejected-protocol', env)
      return 'rejected-protocol'
    }

    // STALENESS: the echoed baseVersion predates our current buffer version and
    // this envelope would mutate the buffer ⇒ the user has typed newer text.
    // Drop it and re-POST the current buffer so the server recomputes on fresh
    // text (the self-correcting loop).
    if (env.baseVersion < version && isMutating(env)) {
      onApply?.('rejected-stale', env)
      resync('resync')
      return 'rejected-stale'
    }

    // SEQ ORDERING: a gap means a dropped SSE event → resync to recover.
    const expected = expectedSeq.get(env.streamId)
    if (expected != null && env.seq > expected) {
      onApply?.('resync', env)
      resync('resync')
    }
    expectedSeq.set(env.streamId, env.seq + 1)

    for (const a of env.actions) {
      const r = applyAction(a)
      if (r !== 'applied') { onApply?.(r, env); return r }
    }
    onApply?.('applied', env)
    return 'applied'
  }

  function onDomEvent(): void {
    // PURE server-side eval: bump the staleness token, then debounce-POST the
    // stabilized buffer. No local evaluation happens here.
    bumpVersion()
    scheduleEval(false, 'input')
  }
  element.addEventListener(event, onDomEvent)

  const unsubscribeSse: Unsubscribe = subscribe
    ? (subscribe('input_action', env => {
        try { applyEnvelope(env) } catch (err) { onError?.(err) }
      }) ?? (() => {}))
    : openOwnedSse({ base, device, EventSourceImpl, reconnect, onEnvelope: applyEnvelope, onError })

  return function unsubscribe() {
    if (closed) return
    closed = true
    if (settleTimer) clearTimeout(settleTimer)
    element.removeEventListener(event, onDomEvent)
    try { unsubscribeSse() } catch { /* best-effort */ }
  }
}
