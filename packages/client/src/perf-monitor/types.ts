// ── Parlay Performance Monitor — shared types ────────────────────────────────

export interface PerfSample {
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

export interface SessionMetrics {
  sessionId: string
  startedAt: number
  samples: PerfSample[]
  summary?: {
    avgRttMs: number
    maxRttMs: number
    avgEngineMs: number
    bottleneck: 'network' | 'js' | 'debounce' | 'balanced' | 'device' | 'stt'
    recommendation: string
  }
}
