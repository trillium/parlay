import { esc, fmtTLTime, TOOL_ICONS, TL_MAX } from './config'
import { tlEntries, tlAtBottom, toolLogVisible, setToolLogVisible, setTlAtBottom, activeChannel } from './state'
import { toolLog, logBtn, thread } from './dom'

// Tool events carry the channel of the agent that produced them. An entry is
// visible only in that agent's tab; the shared 'system' tab shows unattributed
// (non-agent) sessions. Legacy channel-less events show in any tab.
function inActiveTab(ev: any): boolean {
  if (activeChannel === null) return true   // zero-agent state
  return !ev.channel || ev.channel === activeChannel
}

function entryEl(ev: any): HTMLElement {
  const icon = TOOL_ICONS[ev.tool] || '○'
  const el = document.createElement('div')
  el.className = 'pa-tl-entry'

  const lines: string[] = []
  if (ev.cmd) lines.push(esc(ev.cmd))
  if (ev.out) lines.push(`<span class="pa-tl-out">${esc(ev.out)}</span>`)
  if (ev.err) lines.push(`<span style="color:var(--pa-red);opacity:.7">${esc(ev.err)}</span>`)

  el.innerHTML = `
    <div class="pa-tl-head">
      <span class="pa-tl-icon">${icon}</span>
      <span class="pa-tl-tool">${esc(ev.tool)}</span>
      <span class="pa-tl-desc">${esc(ev.desc || '')}</span>
      <span class="pa-tl-ts">${fmtTLTime(ev.ts)}</span>
    </div>
    ${lines.length ? `<div class="pa-tl-body">${lines.join('\n')}</div>` : ''}`
  return el
}

export function appendToolEntry(ev: any) {
  tlEntries.push(ev)
  if (tlEntries.length > TL_MAX) tlEntries.shift()
  if (!inActiveTab(ev)) return   // belongs to another agent's tab — stored, not shown here
  toolLog.appendChild(entryEl(ev))
  if (tlAtBottom) toolLog.scrollTop = toolLog.scrollHeight
}

// Rebuild the log for the active tab — called on open and on tab switch so the
// tool stream stays scoped to whichever agent's tab is showing.
export function renderToolLog() {
  toolLog.innerHTML = ''
  for (const ev of tlEntries) if (inActiveTab(ev)) toolLog.appendChild(entryEl(ev))
  toolLog.scrollTop = toolLog.scrollHeight
}

export function toggleToolLog() {
  const next = !toolLogVisible
  setToolLogVisible(next)
  logBtn.classList.toggle('active', next)
  toolLog.classList.toggle('visible', next)
  thread.style.display = next ? 'none' : ''
  if (next) {
    renderToolLog()
    setTlAtBottom(true)
  }
}

export function wireToolLogEvents() {
  toolLog.addEventListener('scroll', () => {
    setTlAtBottom(toolLog.scrollTop + toolLog.clientHeight >= toolLog.scrollHeight - 50)
  })
  logBtn.addEventListener('click', toggleToolLog)
}
