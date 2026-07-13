// ── Shared mutable state ─────────────────────────────────────────────────────

export interface AgentInfo {
  id:    string
  name:  string
  color: string
}

// Multi-agent tabs
export const agentInfo      = new Map<string, AgentInfo>()  // id → { id, name, color }
export let activeChannel: string | null = null              // null = All view
export const unreadByChannel: Record<string, number> = {}  // id → count

export function setActiveChannel(ch: string | null) {
  activeChannel = ch
}

// Chat state
export const msgs: any[]   = []
export let open             = false
export let annotate         = false
export let unread           = 0
export let atBottom         = true
export let thinking         = false
export let lastId: string | null = null
export let es: EventSource | null = null

export function setOpen(v: boolean) { open = v }
export function setAnnotate(v: boolean) { annotate = v }
export function setUnread(v: number) { unread = v }
export function setAtBottom(v: boolean) { atBottom = v }
export function setThinkingState(v: boolean) { thinking = v }
export function setLastId(v: string | null) { lastId = v }
export function setEs(v: EventSource | null) { es = v }

// Annotation state
export const annotations: Array<{ elementText: string; note: string; el: HTMLElement }> = []
export let hoverEl: HTMLElement | null = null
export let annotateTarget: HTMLElement | null = null
export const markerMap = new WeakMap<HTMLElement, HTMLElement>()

export function setHoverEl(v: HTMLElement | null) { hoverEl = v }
export function setAnnotateTarget(v: HTMLElement | null) { annotateTarget = v }

// Tool log state
export let toolLogVisible = false
export let tlAtBottom = true
export const tlEntries: any[] = []

export function setToolLogVisible(v: boolean) { toolLogVisible = v }
export function setTlAtBottom(v: boolean) { tlAtBottom = v }

// TTS state
export let ttsEnabled = false
export let ttsVoice: SpeechSynthesisVoice | null = null

export function setTtsEnabled(v: boolean) { ttsEnabled = v }
export function setTtsVoice(v: SpeechSynthesisVoice | null) { ttsVoice = v }

// Draft timer
export let draftSaveTimer: ReturnType<typeof setTimeout> | null = null
export function setDraftSaveTimer(v: ReturnType<typeof setTimeout> | null) { draftSaveTimer = v }

// Talon timer
export let talonTimer: ReturnType<typeof setTimeout> | null = null
export function setTalonTimer(v: ReturnType<typeof setTimeout> | null) { talonTimer = v }

// Compact timer
export let compactTimer: ReturnType<typeof setTimeout> | null = null
export function setCompactTimer(v: ReturnType<typeof setTimeout> | null) { compactTimer = v }

// Reconnect state
export let reconnectDelay = 1000
export let reconnectTimer: ReturnType<typeof setTimeout> | null = null
export function setReconnectDelay(v: number) { reconnectDelay = v }
export function setReconnectTimer(v: ReturnType<typeof setTimeout> | null) { reconnectTimer = v }
