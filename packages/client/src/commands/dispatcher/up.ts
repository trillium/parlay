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

// ── Disabled/unreachable-server fallback (robots-q6u) ──────────────────────────
// When the input event routes to the server branch but the server declines to
// evaluate — flag flipped OFF server-side ({disabled:true}), an error response,
// or an unreachable/timed-out relay — the client would otherwise do NOTHING for
// that keystroke, and because the local command pass was skipped, ALL commands
// (clear/submit/tab) silently die until a hard reload. That strands a
// half-reloaded client on a stale cached serverEvalEnabled=true. The fix: run
// the LOCAL command pass for that keystroke so commands always work. Wired by
// initCommands() to runCommandPass; inert (no-op) until wired.
let _disabledFallback: ((text: string) => void) | null = null
export function setEvalDisabledFallback(fn: (text: string) => void): void {
  _disabledFallback = fn
}
function runDisabledFallback(text: string): void {
  try { _disabledFallback?.(text) } catch { /* a fallback must never break input */ }
}

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
    if (!r.ok) { log('eval POST failed', r.status); runDisabledFallback(text); return }
    const body = await r.json()
    if (body?.disabled) { runDisabledFallback(text); return }   // flag off server-side → local pass
    // The synchronous response also carries timing; the ACTIONS are applied via
    // the SSE path (single source of truth) so ordering/staleness is uniform.
    if (body?.timing?.engineEvalNs != null) {
      telemetry.lastEngineEvalNs = body.timing.engineEvalNs
      telemetry.lastRelayMs = body.timing.relayMs ?? telemetry.lastRelayMs
      renderOverlay()
    }
  } catch (e) {
    log('eval POST error', e)
    runDisabledFallback(text)   // unreachable/timed-out relay → never leave commands dead
  }
}
