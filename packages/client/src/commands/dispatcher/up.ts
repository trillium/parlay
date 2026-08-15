import type { EvalCtx } from './types'
import { telemetry, renderOverlay, log } from './telemetry'
import { PA_VERSION } from '../../version'

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

// Base URL prefixed onto the eval POST. Empty by default — the in-page panel
// (init.ts) is served BY the parlay server, so a relative '/api/chat/eval' is
// correct there. External hosts (e.g. herdr-web importing this as a library)
// run on a different origin and must point this at the running parlay server.
//
// Pointing at it is necessary but not sufficient: /api/chat/eval is behind the
// server's origin guard, and this POST sends Content-Type: application/json,
// so from a cross-origin host it PREFLIGHTS and the preflight is refused with
// 403 unless the host's origin is same-origin, loopback, .local, private-LAN,
// or listed in PARLAY_ALLOWED_ORIGINS on the server. The refusal carries no
// CORS headers, so it surfaces here as a rejected fetch that looks exactly
// like the server being down. There is no client-side opt-in, by design — see
// packages/input/README.md and docs/api-contract.md § Origin guard.
let _baseUrl = ''
export function setEvalServerBaseUrl(url: string): void { _baseUrl = url.replace(/\/+$/, '') }

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
    const r = await fetch(`${_baseUrl}/api/chat/eval`, {
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
        paVersion: PA_VERSION,
      }),
      signal: AbortSignal.timeout(3_000),
    })
    if (!r.ok) { log('eval POST failed', r.status); return }
    const body = await r.json()
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
