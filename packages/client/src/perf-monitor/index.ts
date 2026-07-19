// ── Parlay Performance Monitor ────────────────────────────────────────────────
// Automatically collects keystroke telemetry (RTT, engine time, frame rate, device state)
// across all sessions. Analyzes data to identify performance bottlenecks.
//
// Data flows: keystroke event → telemetry sample → persistent storage → analysis

import type { EvalTelemetry } from '../commands/dispatcher/telemetry'
import type { PerfSample, SessionMetrics } from './types'
import { getDeviceInfo, getBatteryLevel, isBatteryLow, isLowPowerMode, getMemoryUsage, getNetworkType } from './device'
import { analyzeAndReport } from './analysis'

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
        analyzeAndReport(metrics)
      }
    }

    // Call original if it exists
    originalRenderOverlay?.()
  }
}

export function getAnalysis(): SessionMetrics {
  return metrics
}
