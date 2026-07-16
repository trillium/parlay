import type { CommandContext } from '../types'
import type { Action, ActionEnvelope, ApplyResult } from './types'
import { PROTOCOL_V } from './types'
import { telemetry, renderOverlay, log, showCountdownHint, clearCountdownHint } from './telemetry'
import { currentInputVersion, postSentAt } from './up'
import { openChannelPicker, closeChannelPicker, pickerHint } from '../../channel-picker'

// ── Down-channel: apply an input_action envelope ───────────────────────────────

// Per-stream expected seq (strict ordering). A gap ⇒ dropped SSE event ⇒ resync.
const expectedSeq = new Map<string, number>()

let _ctx: CommandContext | null = null
export function setDispatcherContext(ctx: CommandContext): void { _ctx = ctx }

export function applyEnvelope(env: ActionEnvelope, resync: (reason: string) => void): ApplyResult {
  if (env.v !== PROTOCOL_V) { log('protocol mismatch', env.v); return 'rejected-protocol' }

  // Round-trip timing: match this envelope's baseVersion to the POST that sent it.
  const sentAt = postSentAt.get(env.baseVersion)
  if (sentAt != null) {
    const rtt = performance.now() - sentAt
    telemetry.lastRoundTripMs = rtt
    if (rtt > telemetry.maxRoundTripMs) telemetry.maxRoundTripMs = rtt
    postSentAt.delete(env.baseVersion)
  }
  if (env.timing?.engineEvalNs != null) telemetry.lastEngineEvalNs = env.timing.engineEvalNs
  if (env.timing?.relayMs != null) telemetry.lastRelayMs = env.timing.relayMs
  if (env.timing?.serverOwnedFire) telemetry.serverOwnedFires++

  // STALENESS: the echoed baseVersion is OLDER than our current input version ⇒
  // the buffer has moved on. Disregard AND immediately re-POST current content so
  // the server recomputes on fresh text (the self-correcting loop).
  if (env.baseVersion < currentInputVersion() && isMutating(env)) {
    telemetry.rejectedStale++
    renderOverlay()
    log('stale action dropped (base', env.baseVersion, '< current', currentInputVersion(), ') → resync')
    resync('resync')
    telemetry.resyncs++
    return 'rejected-stale'
  }

  // SEQ ORDERING: a gap means a dropped SSE event → resync to recover.
  const expected = expectedSeq.get(env.streamId)
  if (expected != null && env.seq > expected) {
    telemetry.seqGaps++
    renderOverlay()
    log('seq gap: expected', expected, 'got', env.seq, '→ resync')
    resync('resync')
    telemetry.resyncs++
  }
  expectedSeq.set(env.streamId, env.seq + 1)

  for (const a of env.actions) {
    const r = applyAction(a)
    if (r !== 'applied') { renderOverlay(); return r }
  }
  telemetry.applied++
  renderOverlay()
  return 'applied'
}

function isMutating(env: ActionEnvelope): boolean {
  return env.actions.some(a =>
    a.verb === 'setText' || a.verb === 'clear' || a.verb === 'submitNow' ||
    a.verb === 'replaceRange' || a.verb === 'stripTrigger',
  )
}

let _armedCountdownTimer: ReturnType<typeof setTimeout> | null = null

// applyAction dispatches ONE action against the CommandContext. Wrapped so a
// single bad action never breaks input (mirrors registry.ts:102 try/catch).
function applyAction(a: Action): ApplyResult {
  if (!_ctx) return 'rejected-protocol'
  try {
    switch (a.verb) {
      case 'noop':
        return 'applied'
      case 'setText':
        _ctx.input.setText(a.args?.text ?? '')
        return 'applied'
      case 'clear':
        _ctx.input.clear()
        return 'applied'
      case 'submitNow':
        return applySubmitNow(a)
      case 'armTimer': {
        // Advisory only: render a local "sending in 1s…" countdown. The
        // AUTHORITATIVE timer is server-side; this NEVER submits on its own.
        if (_armedCountdownTimer) clearTimeout(_armedCountdownTimer)
        const ms = a.args?.fireInMs ?? 1000
        _armedCountdownTimer = setTimeout(() => { /* visual only */ }, ms)
        return 'applied'
      }
      case 'cancelTimer':
        if (_armedCountdownTimer) { clearTimeout(_armedCountdownTimer); _armedCountdownTimer = null }
        clearCountdownHint()
        return 'applied'
      case 'showHint':
        showCountdownHint(a.args?.text ?? '')
        return 'applied'
      case 'clearHint':
        clearCountdownHint()
        return 'applied'
      case 'switchTab':
        if (a.args?.channel) _ctx.tabs.switch(a.args.channel)
        return 'applied'
      case 'openChannelPicker':
        // Backend hands the authoritative ordered list; we render our own
        // perception. Empty channels ⇒ nothing to pick — skip rather than show
        // an empty modal.
        if (a.args?.channels?.length)
          openChannelPicker(a.args.prompt ?? 'Say a channel name, nickname, or number', a.args.channels)
        return 'applied'
      case 'closeChannelPicker':
        // Fires after switchTab in a successful-pick batch; the loop's array
        // order guarantees the tab switch lands before we dismiss the modal.
        closeChannelPicker()
        return 'applied'
      case 'pickerHint':
        pickerHint(a.args?.text ?? '')
        return 'applied'
      case 'archiveTab':
        if (a.args?.channel) _ctx.tabs.archive(a.args.channel)
        return 'applied'
      case 'nextTab': _ctx.tabs.next(); return 'applied'
      case 'prevTab': _ctx.tabs.prev(); return 'applied'
      case 'navigate':
        if (a.args?.url) _ctx.workspace.navigate(a.args.url)
        return 'applied'
      case 'openSwitcher':
        if (!document.getElementById('pa-sheet')?.classList.contains('open'))
          document.getElementById('pa-fab')?.click()
        return 'applied'
      case 'stopSpeech': _ctx.speech.stop(); return 'applied'
      case 'flagSpeech':
        ;(window as any).__paFlagLastSpoken?.()
        return 'applied'
      default:
        log('unknown verb', a.verb)
        return 'applied'   // forward-compat: ignore unknown verbs, don't wedge
    }
  } catch (e) {
    log('action threw', a.verb, e)
    return 'applied'   // an action must never break input
  }
}

// applySubmitNow is the irreversibility guard (brain-v4vje §3): the server DECIDED
// to submit, but its decision is ~1 round-trip stale. Re-verify the tail against
// our TRULY current buffer before firing. This is the exact race the captain wants
// to feel — on a slow link the tail has often already moved.
function applySubmitNow(a: Action): ApplyResult {
  if (!_ctx) return 'rejected-protocol'
  const requireTail = a.args?.requireTail ?? ''
  const val = _ctx.input.value()
  if (requireTail) {
    const idx = val.toLowerCase().lastIndexOf(requireTail.toLowerCase())
    const after = idx === -1 ? '' : val.slice(idx + requireTail.length).trim().replace(/[.!?,;]+/g, '')
    if (idx === -1 || after !== '') {
      log('submitNow REJECTED at apply — tail', JSON.stringify(requireTail), 'no longer at end of', JSON.stringify(val))
      clearCountdownHint()
      return 'rejected-stale'
    }
    const stripped = val.slice(0, idx).trim()
    clearCountdownHint()
    if (stripped) _ctx.input.submit(stripped)
    return 'applied'
  }
  const t = (a.args?.text ?? '').trim()
  clearCountdownHint()
  if (t) _ctx.input.submit(t)
  return 'applied'
}
