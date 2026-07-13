// ── Chat types ──────────────────────────────────────────────────────────────

export interface ChatMessage {
  id:      string
  role:    "user" | "agent"
  ts:      string
  text:    string
  channel?: string   // agent id for agent messages; undefined for user messages
  type?:   "alert"   // set on server-originated alerts, never on captain messages
}

export interface AgentInfo {
  id:    string
  name:  string
  color: string
}

export type SSEClient = {
  id:         string
  controller: ReadableStreamDefaultController
}

export type PollWaiter = {
  resolve:  (msg: ChatMessage) => void
  timer:    ReturnType<typeof setTimeout>
  channel?: string   // when set, only receives messages with matching channel
}
