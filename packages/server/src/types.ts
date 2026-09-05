import type { CapabilityDeclaration } from "./capability"

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
  id:       string
  name:     string
  color:    string
  nicknames?: string[] // human-friendly aliases; first entry is the primary display name
  urls?:     string[]  // pulse pages this agent owns or generated
  path?:     string[]  // filesystem paths this agent is responsible for
  // Launch record (task-4dz9): set once by a Parlay-initiated spawn (`parlay spawn`,
  // ticket auto-claim) so the idle reaper (./prune/idle-reap.ts) can find and act
  // on exactly the agents Parlay itself launched — never a firstmate-spawned or
  // hand-registered one, which carries neither field and is therefore untouched.
  launchedBy?: string // e.g. "parlay-spawn" | "parlay-claim"; absent = not Parlay-launched
  startedAt?:  string // ISO8601, stamped on first registration only — never overwritten
}

export type SSEClient = {
  id:          string
  controller:  ReadableStreamDefaultController
  device?:     string   // client-generated localStorage uuid (?device= on /events)
  ua?:         string   // user-agent header, human-readable device label
  connectedAt: string
  caps?:       CapabilityDeclaration   // validated ?caps= declaration; absent = legacy, gated by nothing
}

export type PollWaiter = {
  resolve:  (msg: ChatMessage | { gone: true }) => void
  timer:    ReturnType<typeof setTimeout>
  channel?: string   // when set, only receives messages with matching channel
}
