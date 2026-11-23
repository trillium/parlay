import { connBanner } from './dom'

export const SEND_FAILED_MSG = 'send failed — your message is still in the box, try again'

// A dropped send must be visible: the text stays in the box, so without this
// it looks identical to a send that is still in flight.
export function showSendFailed() {
  connBanner.className = 'pa-conn-banner reconnecting show'
  connBanner.textContent = SEND_FAILED_MSG
}

export function clearSendFailed() {
  if (connBanner.textContent === SEND_FAILED_MSG) { connBanner.className = 'pa-conn-banner'; connBanner.textContent = '' }
}
