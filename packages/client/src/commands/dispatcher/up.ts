import type { EvalCtx } from './types'
import { telemetry, renderOverlay, log } from './telemetry'

// ── Up-channel: client-owned version + voice-settle-debounced POST ─────────────

// Client-owned monotonic input version. Bumped on EVERY local buffer mutation.
// This is the "timestamp before most-recent change → disregard" semantic the
// captain described, as a collision-free integer counter (never wall-clock, so
// same-millisecond edits can't collide).
let inputVersion = 0
export function bumpInputVersion(): number { return ++inputVersion }
export function currentInputVersion(): number { return inputVersion }

// POST timestamps keyed by the version we sent, so an inbound action can compute
// the true client-observed round trip (POST → SSE receipt).
export const postSentAt = new Map<number, number>()

let _settleTimer: ReturnType<typeof setTimeout> | null = null

// scheduleEval schedules an eval POST after the voice-settle quiet period. Call
// on every input event; rapid dictation bursts collapse into ONE evaluation of
// the STABILIZED text — the server never sees mid-correction text.
export function scheduleEval(
  getText: () => string,
  getCtx: () => EvalCtx,
  immediate = false,
  reason = 'input',
): void {
  const fire = () => void postEval(getText(), getCtx(), reason)
  if (immediate) { fire(); return }
  const { settleMs } = getCtx()
  if (_settleTimer) clearTimeout(_settleTimer)
  _settleTimer = setTimeout(fire, Math.max(0, settleMs))
}

async function postEval(text: string, ctx: EvalCtx, reason: string): Promise<void> {
  const version = currentInputVersion()
  telemetry.posts++
  postSentAt.set(version, performance.now())
  if (postSentAt.size > 64) {
    const oldest = Math.min(...postSentAt.keys())
    postSentAt.delete(oldest)
  }
  try {
    const r = await fetch('/api/chat/eval', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        streamId: ctx.streamId,
        version,
        text,
        cursor: { anchor: 0, active: 0 },
        reason,
        voiceEnabled: ctx.voiceEnabled,
        tabs: ctx.tabs,
        device: ctx.device,
      }),
      signal: AbortSignal.timeout(3_000),
    })
    if (!r.ok) { log('eval POST failed', r.status); return }
    const body = await r.json()
    if (body?.disabled) return   // flag off server-side — do nothing
    // The synchronous response also carries timing; the ACTIONS are applied via
    // the SSE path (single source of truth) so ordering/staleness is uniform.
    if (body?.timing?.engineEvalNs != null) {
      telemetry.lastEngineEvalNs = body.timing.engineEvalNs
      telemetry.lastRelayMs = body.timing.relayMs ?? telemetry.lastRelayMs
      renderOverlay()
    }
  } catch (e) {
    log('eval POST error', e)
  }
}
