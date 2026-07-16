export function reportTtsEvent(type: string, data: Record<string, unknown> = {}) {
  const device = (window as any).__paDeviceId ?? 'unknown'
  fetch('/api/chat/tts-event', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ type, device, ...data }),
  }).catch(() => {})
}
