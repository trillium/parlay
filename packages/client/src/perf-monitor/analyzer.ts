import type { PerfSample, SessionMetrics } from './types'

export function analyzeMetrics(metrics: SessionMetrics): void {
  if (metrics.samples.length === 0) return

  const rttTimes = metrics.samples.map(s => s.rttMs)
  const engineTimes = metrics.samples.map(s => s.engineMs)
  const settleTimes = metrics.samples.map(s => s.settleMs)
  const memoryUsages = metrics.samples.map(s => s.device.memoryHeapUsedMB).filter(m => m !== undefined) as number[]

  // STT correlation: split samples by STT activity
  const sttActiveSamples = metrics.samples.filter(s => s.sttActive)
  const sttInactiveSamples = metrics.samples.filter(s => !s.sttActive)
  const avgRttWithStt = sttActiveSamples.length > 0
    ? sttActiveSamples.map(s => s.rttMs).reduce((a, b) => a + b, 0) / sttActiveSamples.length
    : undefined
  const avgRttWithoutStt = sttInactiveSamples.length > 0
    ? sttInactiveSamples.map(s => s.rttMs).reduce((a, b) => a + b, 0) / sttInactiveSamples.length
    : undefined
  const sttCorrelation = (avgRttWithStt && avgRttWithoutStt)
    ? (avgRttWithStt / avgRttWithoutStt).toFixed(2)
    : undefined

  const avgRtt = rttTimes.reduce((a, b) => a + b, 0) / rttTimes.length
  const maxRtt = Math.max(...rttTimes)
  const avgEngine = engineTimes.reduce((a, b) => a + b, 0) / engineTimes.length
  const avgSettle = settleTimes.reduce((a, b) => a + b, 0) / settleTimes.length
  const avgMemory = memoryUsages.length > 0 ? memoryUsages.reduce((a, b) => a + b, 0) / memoryUsages.length : undefined

  // Determine bottleneck
  let bottleneck: 'network' | 'js' | 'debounce' | 'balanced' | 'device' | 'stt'
  let recommendation = ''

  // STT contention: if lag is significantly worse during active speech recognition
  if (sttCorrelation && parseFloat(sttCorrelation) > 1.5 && sttActiveSamples.length > 0) {
    bottleneck = 'stt'
    recommendation = `Speech-to-text is consuming CPU, causing ${sttCorrelation}× slower keystroke response. STT and typing are competing for the main thread. Try: disable STT during typing, or use a background STT service that doesn't block the main thread.`
  } else if (avgMemory && avgMemory > 120) {
    bottleneck = 'device'
    recommendation = `High memory usage (${avgMemory.toFixed(0)}MB heap). JavaScript bundle or TTS plugin consuming too much memory. Try disabling TTS plugin to reduce heap pressure.`
  } else if (avgRtt > avgEngine * 10) {
    bottleneck = 'network'
    recommendation = `Network latency (${avgRtt.toFixed(0)}ms RTT) is the main bottleneck. Try optimizing server location or using a CDN.`
  } else if (avgEngine > 50) {
    bottleneck = 'js'
    recommendation = `JavaScript evaluation (${avgEngine.toFixed(1)}ms) is slow. Consider disabling TTS plugin or optimizing eval engine.`
  } else if (avgSettle > 300) {
    bottleneck = 'debounce'
    recommendation = `Voice settle debounce (${avgSettle.toFixed(0)}ms) is high. Try lowering to 250ms if dictation accuracy isn't critical.`
  } else {
    bottleneck = 'balanced'
    recommendation = 'Performance is balanced. No single bottleneck detected.'
  }

  metrics.summary = {
    avgRttMs: avgRtt,
    maxRttMs: maxRtt,
    avgEngineMs: avgEngine,
    bottleneck,
    recommendation,
  }
}

export function displayAnalysis(metrics: SessionMetrics): void {
  if (!metrics.summary) return

  const memSamples = metrics.samples.map(s => s.device.memoryHeapUsedMB).filter(m => m !== undefined) as number[]
  const avgMem = memSamples.length > 0 ? (memSamples.reduce((a, b) => a + b, 0) / memSamples.length).toFixed(0) : 'N/A'
  const battery = metrics.samples[0]?.device.battery
  const batteryStatus = battery ? `${Math.round(battery)}%` : 'N/A'

  const sttActiveSamples = metrics.samples.filter(s => s.sttActive)
  const sttInactiveSamples = metrics.samples.filter(s => !s.sttActive)
  const sttSection = sttActiveSamples.length > 0
    ? (() => {
        const avgWithStt = sttActiveSamples.map(s => s.rttMs).reduce((a, b) => a + b, 0) / sttActiveSamples.length
        const avgWithoutStt = sttInactiveSamples.length > 0
          ? sttInactiveSamples.map(s => s.rttMs).reduce((a, b) => a + b, 0) / sttInactiveSamples.length
          : avgWithStt
        const ratio = (avgWithStt / avgWithoutStt).toFixed(2)
        return `\nSTT CORRELATION:
- Avg RTT with STT active: ${avgWithStt.toFixed(0)}ms
- Avg RTT without STT: ${avgWithoutStt.toFixed(0)}ms
- Correlation ratio: ${ratio}×`
      })()
    : ''

  const msg = `
🔍 PERFORMANCE ANALYSIS (${metrics.samples.length} keystrokes)

DEVICE TELEMETRY:
- Battery: ${batteryStatus}
- Memory Heap: ${avgMem}MB (Chrome/Blink only; N/A on Safari)
- STT active during: ${sttActiveSamples.length} / ${metrics.samples.length} samples${sttSection}

BOTTLENECK DIAGNOSIS: ${metrics.summary.bottleneck.toUpperCase()}
- Avg RTT: ${metrics.summary.avgRttMs.toFixed(0)}ms (max ${metrics.summary.maxRttMs.toFixed(0)}ms)
- Avg Engine: ${metrics.summary.avgEngineMs.toFixed(1)}ms

RECOMMENDATION: ${metrics.summary.recommendation}
  `.trim()

  console.log(msg)
  ;(window as any).__paAnalysisMessage = msg
}
