// Input timing telemetry — extracted from input.ts (250-line split)
// Measures event-to-first-frame latency as a proxy for browser responsiveness.
// Sampled every 5th keystroke; flushed in batches so measurement adds no per-keystroke cost.

let _lastInputTs = 0, _sampleN = 0
const _timingBatch: Array<{ sinceLastMs: number; costMs: number }> = []
let _flushTimer: ReturnType<typeof setTimeout> | null = null

function _flushTiming(deviceId: string) {
  if (_flushTimer) { clearTimeout(_flushTimer); _flushTimer = null }
  if (!_timingBatch.length) return
  const samples = _timingBatch.splice(0)
  fetch('/api/debug/input-timing', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ device: deviceId, ua: navigator.userAgent, samples }),
  }).catch(() => {})
}

export function sampleInput(deviceId: string) {
  const now = performance.now()
  const sinceLastMs = _lastInputTs ? now - _lastInputTs : 0
  _lastInputTs = now
  if (++_sampleN % 5 !== 0) return
  const t0 = now
  requestAnimationFrame(() => {
    _timingBatch.push({ sinceLastMs, costMs: performance.now() - t0 })
    if (_timingBatch.length >= 20) _flushTiming(deviceId)
    else { if (_flushTimer) clearTimeout(_flushTimer); _flushTimer = setTimeout(() => _flushTiming(deviceId), 5000) }
  })
}
