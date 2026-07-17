// ── Picker initialization (channel + sender voice pickers) ─────────────────────

import { injectPageNav, openPageNav } from './page-nav'
import { injectCommandsModal } from './commands-modal'
import { injectChannelPickerStyles } from './channel-picker'
import { injectSenderPickerStyles } from './sender-picker'

export function initPickers(): void {
  injectPageNav()
  injectCommandsModal()
  injectChannelPickerStyles()
  injectSenderPickerStyles()
  document.getElementById('pa-nav-btn')?.addEventListener('click', openPageNav)
}
