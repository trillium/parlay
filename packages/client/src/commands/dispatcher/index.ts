// ── Client action dispatcher (feat/server-side-eval) — barrel ──────────────────
//
// In the PURE server-side model the client does NO evaluation. It:
//   1. POSTs the (voice-settled) box content up to /api/chat/eval, carrying a
//      monotonic client-owned input VERSION (up.ts).
//   2. Receives input_action SSE envelopes carrying actions computed by the
//      compiled Go engine.
//   3. Validates each envelope — protocol version, seq order, staleness — and
//      applies the actions through the SAME CommandContext the local commands use
//      (apply.ts), with version-based staleness rejection + auto-resync.
//
// The instrumentation (telemetry.ts) surfaces round-trip latency, the compiled
// eval time, reconciliations, and rejected stale actions in a live overlay so the
// captain can OBSERVE the complexity.

export type { Action, ActionEnvelope, ApplyResult, EvalCtx } from './types'
export { telemetry, renderOverlay } from './telemetry'
export { bumpInputVersion, currentInputVersion, scheduleEval, setEvalServerBaseUrl } from './up'
export { setDispatcherContext, applyEnvelope } from './apply'
