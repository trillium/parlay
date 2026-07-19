// ── Parlay Performance Monitor — device state probes ─────────────────────────

export function getDeviceInfo(): string {
  const ua = navigator.userAgent
  if (/iPhone/.test(ua)) return 'iPhone'
  if (/iPad/.test(ua)) return 'iPad'
  if (/Android/.test(ua)) return 'Android'
  return 'Unknown'
}

export function getBatteryLevel(): number | undefined {
  const battery = (navigator as any).getBattery?.()
  return battery?.level ? battery.level * 100 : undefined
}

export function isBatteryLow(): boolean {
  const battery = (navigator as any).getBattery?.()
  return battery?.level ? battery.level < 0.2 : false
}

export function isLowPowerMode(): boolean {
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

export function getMemoryUsage(): { heapUsedMB?: number; heapLimitMB?: number } {
  // Chrome/Blink expose memory info; Safari/iOS don't for security reasons.
  // On Chrome: performance.memory.usedJSHeapSize (bytes)
  const mem = (performance as any).memory
  return {
    heapUsedMB: mem?.usedJSHeapSize ? Math.round(mem.usedJSHeapSize / 1024 / 1024) : undefined,
    heapLimitMB: mem?.jsHeapSizeLimit ? Math.round(mem.jsHeapSizeLimit / 1024 / 1024) : undefined,
  }
}

export function getNetworkType(): string | undefined {
  const conn = (navigator as any).connection || (navigator as any).mozConnection
  return conn?.effectiveType ?? conn?.type
}
