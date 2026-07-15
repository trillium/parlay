// ── Instrumentation — the captain must be able to OBSERVE the complexity ───────
// A live overlay + console logging so the round-trip latency, the compiled-eval
// time, the version reconciliations, and the rejected stale actions are all
// visible without opening devtools. Observing the cost is the whole point.

export interface EvalTelemetry {
  posts: number
  applied: number
  rejectedStale: number
  rejectedExpired: number
  resyncs: number
  seqGaps: number
  serverOwnedFires: number
  lastEngineEvalNs: number
  lastRelayMs: number
  lastRoundTripMs: number   // client-measured: POST send → SSE action receipt
  maxRoundTripMs: number
}

export const telemetry: EvalTelemetry = {
  posts: 0, applied: 0, rejectedStale: 0, rejectedExpired: 0, resyncs: 0,
  seqGaps: 0, serverOwnedFires: 0,
  lastEngineEvalNs: 0, lastRelayMs: 0, lastRoundTripMs: 0, maxRoundTripMs: 0,
}

let overlayEl: HTMLElement | null = null
function ensureOverlay(): HTMLElement {
  if (overlayEl) return overlayEl
  const el = document.createElement('div')
  el.id = 'pa-eval-overlay'
  el.style.cssText = [
    // Anchored to the RIGHT gutter, pushed below the fixed header/tabs band and
    // well above the bottom-anchored input box, so it never covers either.
    // Tap to dismiss so it is never stuck in the way. `bottom/left:auto` guard
    // against any inherited/UA anchoring.
    'position:fixed', 'top:calc(env(safe-area-inset-top, 0px) + 56px)',
    'right:6px', 'left:auto', 'bottom:auto', 'z-index:2147483647',
    'font:10px/1.35 ui-monospace,Menlo,monospace', 'color:#9fe',
    'background:rgba(0,0,0,.82)', 'padding:6px 8px', 'border-radius:6px',
    'pointer-events:auto', 'cursor:pointer', 'white-space:pre', 'max-width:46ch',
    'border:1px solid #2a6', 'box-shadow:0 2px 10px rgba(0,0,0,.5)',
  ].join(';')
  el.title = 'tap to hide server-eval telemetry'
  el.addEventListener('click', () => { el.style.display = 'none' })
  document.body.appendChild(el)
  overlayEl = el
  return el
}

export function renderOverlay(): void {
  const t = telemetry
  const el = ensureOverlay()
  const engMs = t.lastEngineEvalNs / 1e6
  el.textContent =
    `SERVER-EVAL (compiled Go)\n` +
    `engine ${engMs.toFixed(3)}ms | relay ${t.lastRelayMs.toFixed(1)}ms | RTT ${t.lastRoundTripMs.toFixed(0)}ms (max ${t.maxRoundTripMs.toFixed(0)})\n` +
    `posts ${t.posts}  applied ${t.applied}  stale ${t.rejectedStale}  expired ${t.rejectedExpired}\n` +
    `resyncs ${t.resyncs}  seqGaps ${t.seqGaps}  serverFires ${t.serverOwnedFires}`
  // The comparison the captain asked for: is compiled speed still a win after
  // the round trip? Flag it when the network dwarfs the eval.
  if (t.lastRoundTripMs > 0 && engMs > 0) {
    const ratio = t.lastRoundTripMs / engMs
    el.textContent += `\nRTT is ${ratio.toFixed(0)}× the compiled eval time`
  }
}

export function log(...a: unknown[]): void {
  // eslint-disable-next-line no-console
  console.log('%c[server-eval]', 'color:#3fa', ...a)
}

// ── Countdown hint UI (tiny, non-destructive) ──────────────────────────────────
export function showCountdownHint(text: string): void {
  let el = document.getElementById('pa-eval-hint')
  if (!el) {
    el = document.createElement('div')
    el.id = 'pa-eval-hint'
    el.style.cssText = [
      // LEFT gutter, same clear-of-header offset as the overlay. Never over the
      // bottom input, and the opposite side from the right-anchored overlay so
      // the two never collide even as the overlay grows to several lines.
      'position:fixed', 'top:calc(env(safe-area-inset-top, 0px) + 56px)',
      'left:6px', 'right:auto', 'bottom:auto', 'z-index:2147483647',
      'font:11px ui-monospace,monospace', 'color:#fe9', 'background:rgba(40,20,0,.9)',
      'padding:4px 8px', 'border-radius:6px', 'border:1px solid #a63',
      'pointer-events:none', 'max-width:44vw', 'white-space:pre-wrap',
    ].join(';')
    document.body.appendChild(el)
  }
  el.textContent = text
}

export function clearCountdownHint(): void {
  document.getElementById('pa-eval-hint')?.remove()
}
