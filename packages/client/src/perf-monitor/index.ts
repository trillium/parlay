// ── Parlay Performance Monitor ────────────────────────────────────────────────
// Automatically collects keystroke telemetry (RTT, engine time, frame rate, device state)
// across all sessions. Analyzes data to identify performance bottlenecks.
//
// Data flows: keystroke event → telemetry sample → persistent storage → analysis

import type { EvalTelemetry } from '../commands/dispatcher/telemetry'
import { sttState, hookSpeechRecognition } from './stt-detector'
import { analyzeMetrics, displayAnalysis } from './analyzer'
import type { SessionMetrics, PerfSample } from './types'

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

function analyzeAndReport(): void {
  analyzeMetrics(metrics)
  displayAnalysis(metrics)
  void persistMetrics()
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
  const battery = (navigator as any).getBattery?.()
  if (battery?.level && battery.level < 0.2) return true
  return false
}

function getMemoryUsage(): { heapUsedMB?: number; heapLimitMB?: number } {
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

export function getAnalysis(): SessionMetrics {
  return metrics
}
