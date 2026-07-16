import { getSettings } from './io'
import { activeChannel, agentInfo, channelStatus, unreadByChannel, msgs } from '../state'
import { PA_VERSION } from '../version'
import { closeSettingsModal } from './lifecycle'

export async function sendDebugSnapshot() {
  const s = getSettings()
  const sseStates = ['CONNECTING', 'OPEN', 'CLOSED']
  const paEs = (window as any).__paEs as EventSource | null
  const snapshot = {
    pa_version: PA_VERSION,
    device_id: (window as any).__paDeviceId ?? 'unknown',
    sse: {
      state: paEs ? (sseStates[paEs.readyState] ?? 'UNKNOWN') : 'none',
      ready_state: paEs?.readyState ?? -1,
    },
    voice: {
      enabled: s.voiceEnabled,
      submit_phrases: s.voiceSubmitPhrases,
      clear_phrases: s.voiceClearPhrases ?? [],
      settle_ms: s.voiceSettleMs ?? 450,
      local_only: !!s.localOnlyVoice,
    },
    eval_telemetry: (window as any).__parlay?.evalTelemetry?.() ?? null,
    active_channel: activeChannel,
    agents: [...agentInfo.values()].map(a => ({
      id: a.id,
      name: a.name,
      status: channelStatus[a.id] ?? 'offline',
    })),
    unread_by_channel: { ...unreadByChannel },
    message_count: msgs.length,
    input_value: (document.getElementById('pa-input') as HTMLTextAreaElement | null)?.value ?? '',
    cold_start: (window as any).__paColdStart ?? null,
  }
  const text = '**[debug-snapshot]**\n```json\n' + JSON.stringify(snapshot, null, 2) + '\n```'
  closeSettingsModal()
  const toAgent = activeChannel ?? undefined
  await fetch('/api/chat/send', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text, ...(toAgent ? { toAgent } : {}) }),
  }).catch(() => {})
}
