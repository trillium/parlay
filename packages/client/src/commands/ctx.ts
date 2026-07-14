import type { CommandContext } from './types'
import { inputEl } from '../dom'
import { agentInfo, activeChannel } from '../state'
import { switchChannel, archiveChannel, archived } from '../tabs'
import { getSettings } from '../settings-modal'
import { autoResize, sendMsg, clearDraft, scheduleDraftSave } from '../input'

// ── CommandContext implementation ────────────────────────────────────────────
// The ONLY surface command actions touch. Everything routes through existing
// panel modules so commands inherit draft hygiene, tab persistence, etc.

function visibleTabs(): string[] {
  return [...agentInfo.keys()].filter(id => !archived.has(id))
}

export function buildContext(): CommandContext {
  return {
    input: {
      value: () => inputEl.value,
      // setText deliberately does NOT run the command pass — programmatic edits
      // (e.g. Cursorless) must not trip trailing submit words
      setText(t: string) { inputEl.value = t; autoResize(); scheduleDraftSave() },
      clear() { inputEl.value = ''; autoResize(); clearDraft() },
      submit(text: string) { const t = text.trim(); if (t) { inputEl.value = t; autoResize(); void sendMsg(t) } },
      selection: () => ({ anchor: inputEl.selectionStart ?? 0, active: inputEl.selectionEnd ?? 0 }),
      setSelection(anchor: number, active: number) {
        inputEl.setSelectionRange(Math.min(anchor, active), Math.max(anchor, active))
      },
    },
    tabs: {
      list: () => [...agentInfo.values()].map(a => ({ id: a.id, name: a.name })),
      active: () => activeChannel,
      switch(id: string) { if (!agentInfo.has(id)) return false; switchChannel(id); return true },
      archive(id: string) { if (!agentInfo.has(id)) return false; archiveChannel(id); return true },
      next() {
        const ids = visibleTabs()
        if (!ids.length) return
        const i = ids.indexOf(activeChannel ?? '')
        switchChannel(ids[(i + 1) % ids.length])
      },
      prev() {
        const ids = visibleTabs()
        if (!ids.length) return
        const i = ids.indexOf(activeChannel ?? '')
        switchChannel(ids[(i - 1 + ids.length) % ids.length])
      },
    },
    drawer: {
      open() { (window as any).__paOpenDrawer?.() },
    },
    speech: {
      stop() { (window as any).__paStopSpeak?.() },
    },
    settings: { get: getSettings },
    workspace: { navigate: navigateWorkspace, present: workspacePresent },
  }
}

// Parlay-as-shell (#16): drive the workspace iframe when the page has one
// (chat-app) — the chat drawer stays live as persistent chrome, zero SSE
// teardown. Elsewhere, fall back to a full navigation.
export function navigateWorkspace(url: string): boolean {
  const frame = document.getElementById('pa-workspace') as HTMLIFrameElement | null
  const sameOrigin = url.startsWith('/') || url.startsWith(location.origin)
  if (frame && sameOrigin) { frame.src = url; return true }
  location.href = url
  return false   // navigated, but via teardown
}
export function workspacePresent(): boolean {
  return !!document.getElementById('pa-workspace')
}

// Resolve a spoken {agent} capture against live agents: exact id, exact name,
// then substring on either (case-insensitive). Returns the agent id or null.
export function resolveAgent(spoken: string): string | null {
  const q = spoken.trim().toLowerCase().replace(/[.!?,;:]+$/, '')
  if (!q) return null
  for (const a of agentInfo.values()) {
    if (a.id.toLowerCase() === q || a.name.toLowerCase() === q) return a.id
  }
  for (const a of agentInfo.values()) {
    if (a.id.toLowerCase().includes(q) || a.name.toLowerCase().includes(q)) return a.id
  }
  return null
}
