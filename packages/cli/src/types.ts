// parlay CLI shared wire types: the shapes returned by the chat server.

export interface ChatMessage {
  id: string
  role: "user" | "agent"
  ts: string
  text: string
  channel?: string
  type?: "alert"
}

export interface AgentInfo { id: string; name: string; color: string }

export interface SubscribersInfo {
  parlay?: { clients?: number }
  poll?: { count?: number; channels?: Array<{ channel: string | null; id?: string; name?: string }> }
  registered?: { count?: number; agents?: AgentInfo[] }
  presence_broadcasts?: number
}
