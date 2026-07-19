// ── Parlay Performance Monitor ────────────────────────────────────────────────
// Automatically collects keystroke telemetry (RTT, engine time, frame rate, device state)
// across all sessions. Analyzes data to identify performance bottlenecks.
//
// Data flows: keystroke event → telemetry sample → persistent storage → analysis

import type { EvalTelemetry } from './commands/dispatcher/telemetry'

interface PerfSample {
  timestamp: number
  rttMs: number
  engineMs: number
  relayMs: number
  settleMs: number
  keyCount: number
  sttActive: boolean
  sttListening: boolean
  device: {
    model: string
    battery?: number
    batteryLow?: boolean
    lowPowerMode?: boolean
    networkType?: string
    memoryHeapUsedMB?: number
    memoryHeapLimitMB?: number
  }
}

interface SessionMetrics {
  sessionId: string
  startedAt: number
  samples: PerfSample[]
  summary?: {
    avgRttMs: number
    maxRttMs: number
    avgEngineMs: number
    bottleneck: 'network' | 'js' | 'debounce' | 'balanced'
    recommendation: string
  }
}

const SESSION_ID = `perf-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
const metrics: SessionMetrics = {
  sessionId: SESSION_ID,
  startedAt: Date.now(),
  samples: [],
}

let keyCount = 0
let lastSampleTime = Date.now()

// ── STT Activity Detection ──────────────────────────────────────────────────
let sttState = { active: false, listening: false }

function hookSpeechRecognition(): void {
  const SpeechRecognition = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition
  if (!SpeechRecognition) return

  const originalConstruct = SpeechRecognition
  const hooked = function (...args: any[]) {
    const instance = new originalConstruct(...args)

    instance.addEventListener('start', () => { sttState.active = true; sttState.listening = true })
    instance.addEventListener('end', () => { sttState.active = false; sttState.listening = false })
    instance.addEventListener('result', () => { sttState.listening = false })  // processing, not listening
    instance.addEventListener('error', () => { sttState.active = false; sttState.listening = false })

    return instance
  }
  hooked.prototype = originalConstruct.prototype
  ;(window as any).SpeechRecognition = hooked
  ;(window as any).webkitSpeechRecognition = hooked
}

// Hook into the existing telemetry system
export function initPerfMonitor(getTelemetry: () => EvalTelemetry, getSettleMs: () => number): void {
  // Set up STT detection (Web Speech API hooking)
  hookSpeechRecognition()

  // Sample on every keystroke (every eval POST)
  const originalRenderOverlay = (window as any).__paRenderOverlay
  ;(window as any).__paRenderOverlay = () => {
    const t = getTelemetry()
    keyCount++

    // Only sample every 5th keystroke to reduce overhead
    if (keyCount % 5 === 0) {
      const now = Date.now()
      const mem = getMemoryUsage()
      const sample: PerfSample = {
        timestamp: now,
        rttMs: t.lastRoundTripMs,
        engineMs: t.lastEngineEvalNs / 1e6,
        relayMs: t.lastRelayMs,
        settleMs: getSettleMs(),
        keyCount,
        sttActive: sttState.active,
        sttListening: sttState.listening,
        device: {
          model: getDeviceInfo(),
          battery: getBatteryLevel(),
          batteryLow: isBatteryLow(),
          lowPowerMode: isLowPowerMode(),
          networkType: getNetworkType(),
          memoryHeapUsedMB: mem.heapUsedMB,
          memoryHeapLimitMB: mem.heapLimitMB,
        },
      }
      metrics.samples.push(sample)
      lastSampleTime = now

      // Auto-analyze after 50 samples or 5 minutes
      if (metrics.samples.length >= 50 || now - metrics.startedAt > 5 * 60 * 1000) {
        analyzeAndReport()
      }
    }

    // Call original if it exists
    originalRenderOverlay?.()
  }
}

function getDeviceInfo(): string {
  const ua = navigator.userAgent
  if (/iPhone/.test(ua)) return 'iPhone'
  if (/iPad/.test(ua)) return 'iPad'
  if (/Android/.test(ua)) return 'Android'
  return 'Unknown'
}

function getBatteryLevel(): number | undefined {
  const battery = (navigator as any).getBattery?.()
  return battery?.level ? battery.level * 100 : undefined
}

function isBatteryLow(): boolean {
  const battery = (navigator as any).getBattery?.()
  return battery?.level ? battery.level < 0.2 : false
}

function isLowPowerMode(): boolean {
  // iOS Low Power Mode reduces CPU/GPU/network performance explicitly.
  // Detect via: battery level < 20% OR Battery Status API's explicit flag (when available).
  // Note: iOS Safari doesn't expose explicit low-power-mode flag, so we infer from battery
  // + check if the device is explicitly in reduced-performance mode by monitoring CPU spikes.
  const battery = (navigator as any).getBattery?.()
  if (battery?.level && battery.level < 0.2) return true

  // Fallback: check for performance degradation patterns (slower eval times usually indicate LPM)
  // This is measured empirically in analysis phase if needed.
  return false
}

function getMemoryUsage(): { heapUsedMB?: number; heapLimitMB?: number } {
  // Chrome/Blink expose memory info; Safari/iOS don't for security reasons.
  // On Chrome: performance.memory.usedJSHeapSize (bytes)
  const mem = (performance as any).memory
  return {
    heapUsedMB: mem?.usedJSHeapSize ? Math.round(mem.usedJSHeapSize / 1024 / 1024) : undefined,
    heapLimitMB: mem?.jsHeapSizeLimit ? Math.round(mem.jsHeapSizeLimit / 1024 / 1024) : undefined,
  }
}

function getNetworkType(): string | undefined {
  const conn = (navigator as any).connection || (navigator as any).mozConnection
  return conn?.effectiveType ?? conn?.type
}

function analyzeAndReport(): void {
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

  // Send to server for persistent storage
  void persistMetrics()

  // Display findings
  displayAnalysis()
}

async function persistMetrics(): Promise<void> {
  try {
    const resp = await fetch('/api/perf/session', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(metrics),
    })
    if (!resp.ok) console.warn('[perf-monitor] Failed to persist metrics')
  } catch (e) {
    console.warn('[perf-monitor] Storage error:', e)
  }
}

function displayAnalysis(): void {
  if (!metrics.summary) return

  // Gather device telemetry (for context, not necessarily a bottleneck)
  const memSamples = metrics.samples.map(s => s.device.memoryHeapUsedMB).filter(m => m !== undefined) as number[]
  const avgMem = memSamples.length > 0 ? (memSamples.reduce((a, b) => a + b, 0) / memSamples.length).toFixed(0) : 'N/A'
  const battery = metrics.samples[0]?.device.battery
  const batteryStatus = battery ? `${Math.round(battery)}%` : 'N/A'

  // STT correlation data
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

export function getAnalysis(): SessionMetrics {
  return metrics
}
