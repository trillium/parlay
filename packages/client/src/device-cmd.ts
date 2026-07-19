// Agent-triggerable device commands via SSE: reload, reset-tts, ping.
// Agents POST /api/chat/device-cmd to drive the client without needing the
// captain to press anything — useful for live debugging on mobile.

import { onSse } from './sse'
import { agentInfo } from './state'
import { switchChannel } from './tabs'
import { sendDebugSnapshot } from './settings-modal/debug'

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
  })
}
