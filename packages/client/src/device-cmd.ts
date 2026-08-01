// Agent-triggerable device commands via SSE: reload, reset-tts, ping.
// Agents POST /api/chat/device-cmd to drive the client without needing the
// captain to press anything — useful for live debugging on mobile.

import { onSse } from './sse'
import { agentInfo } from './state'
import { switchChannel } from './tabs'
import { sendDebugSnapshot } from './settings-modal/debug'
import { getSettings, saveSettings } from './settings-modal'

export function wireDeviceCommands(openDrawer: (skipFocus?: boolean) => void): void {
  onSse('device_cmd', (data: { cmd: string; args?: Record<string, string> }) => {
    if (data.cmd === 'reload') { location.reload(); return }
    if (data.cmd === 'reset-tts') { (window as any).__paResetTts?.(); return }
    if (data.cmd === 'ping') { void sendDebugSnapshot(); return }
    if (data.cmd === 'switch-channel') {
      const ch = data.args?.channel
      if (ch && agentInfo.has(ch)) { switchChannel(ch); openDrawer() }
      return
    }
    if (data.cmd === 'list-channels') {
      // Agent reads /api/chat/agents server-side; snapshot gives context
      void sendDebugSnapshot()
      return
    }
    if (data.cmd === 'set-hands-free') {
      // Voice-reachable toggle: the captain just asks an agent, no tapping
      // required. args.enabled: 'true'/'false'; omitted flips the current value.
      const raw = data.args?.enabled
      const s = getSettings()
      const on = raw === undefined ? !s.noKeyboardMode : raw === 'true'
      void saveSettings({ ...s, noKeyboardMode: on })
      return
    }
  })
}
