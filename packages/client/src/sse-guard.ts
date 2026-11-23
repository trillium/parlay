// A malformed or unexpected frame must cost one event, not an uncaught throw
// inside the listener: wrap every core SSE handler that parses its payload.
export function guarded(fn: (e: MessageEvent) => void): (e: MessageEvent) => void {
  return (e) => {
    try { fn(e) } catch (err) { console.warn('[parlay] dropped malformed SSE frame', err) }
  }
}
