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
  device: {
    model: string
    battery?: number
    batteryLow?: boolean
    networkType?: string
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

// Hook into the existing telemetry system
export function initPerfMonitor(getTelemetry: () => EvalTelemetry, getSettleMs: () => number): void {
  // Sample on every keystroke (every eval POST)
  const originalRenderOverlay = (window as any).__paRenderOverlay
  ;(window as any).__paRenderOverlay = () => {
    const t = getTelemetry()
    keyCount++

    // Only sample every 5th keystroke to reduce overhead
    if (keyCount % 5 === 0) {
      const now = Date.now()
      const sample: PerfSample = {
        timestamp: now,
        rttMs: t.lastRoundTripMs,
        engineMs: t.lastEngineEvalNs / 1e6,
        relayMs: t.lastRelayMs,
        settleMs: getSettleMs(),
        keyCount,
        device: {
          model: getDeviceInfo(),
          battery: getBatteryLevel(),
          batteryLow: isBatteryLow(),
          networkType: getNetworkType(),
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

function getNetworkType(): string | undefined {
  const conn = (navigator as any).connection || (navigator as any).mozConnection
  return conn?.effectiveType ?? conn?.type
}

function analyzeAndReport(): void {
  if (metrics.samples.length === 0) return

  const rttTimes = metrics.samples.map(s => s.rttMs)
  const engineTimes = metrics.samples.map(s => s.engineMs)
  const settleTimes = metrics.samples.map(s => s.settleMs)

  const avgRtt = rttTimes.reduce((a, b) => a + b, 0) / rttTimes.length
  const maxRtt = Math.max(...rttTimes)
  const avgEngine = engineTimes.reduce((a, b) => a + b, 0) / engineTimes.length
  const avgSettle = settleTimes.reduce((a, b) => a + b, 0) / settleTimes.length

  // Determine bottleneck
  let bottleneck: 'network' | 'js' | 'debounce' | 'balanced'
  let recommendation = ''

  if (avgRtt > avgEngine * 10) {
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

  const msg = `
🔍 PERFORMANCE ANALYSIS (${metrics.samples.length} keystrokes)

Bottleneck: ${metrics.summary.bottleneck.toUpperCase()}
- Avg RTT: ${metrics.summary.avgRttMs.toFixed(0)}ms (max ${metrics.summary.maxRttMs.toFixed(0)}ms)
- Avg Engine: ${metrics.summary.avgEngineMs.toFixed(1)}ms

Recommendation: ${metrics.summary.recommendation}
  `.trim()

  console.log(msg)
  ;(window as any).__paAnalysisMessage = msg
}

export function getAnalysis(): SessionMetrics {
  return metrics
}
