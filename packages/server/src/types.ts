// ── Chat types ──────────────────────────────────────────────────────────────

// Agent-suggested view change — rendered as an inline card; nothing happens
// until the captain clicks, and the effect is local to the clicking device.
export interface ChatAction {
  kind:     "navigate" | "switch_tab"
  url?:     string   // navigate target
  channel?: string   // switch_tab target
  label:    string   // card text
}

export interface ChatMessage {
  id:      string
  role:    "user" | "agent"
  ts:      string
  text:    string
  channel?: string   // agent id for agent messages; undefined for user messages
  type?:   "alert" | "action_request" | "system_update"   // alert: server-originated; action_request: agent suggestion card; system_update: hook/system event line
  action?: ChatAction                    // present iff type === "action_request"
  source?: string                        // system_update: which hook/system emitted it
  meta?:   Record<string, unknown>       // system_update: attribution (e.g. session_id) for future tab mapping
  images?: string[]                      // attached image URLs (uploads or agent-provided), rendered inline
  from?:   string                        // user-role sender attribution (#19): relay/intake senders; absent = the captain
  received?: boolean                     // delivery status of a user message: false=queued, true=agent polled it. Runtime-only, stripped from disk.
}

export interface AgentInfo {
  id:    string
  name:  string
  color: string
}

export type SSEClient = {
  id:          string
  controller:  ReadableStreamDefaultController
  device?:     string   // client-generated localStorage uuid (?device= on /events)
  ua?:         string   // user-agent header, human-readable device label
  connectedAt: string
}

export type PollWaiter = {
  resolve:  (msg: ChatMessage) => void
  timer:    ReturnType<typeof setTimeout>
  channel?: string   // when set, only receives messages with matching channel
}
