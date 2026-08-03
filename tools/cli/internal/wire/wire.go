// Package wire holds the parlay CLI's shared wire types: the shapes
// returned by the Pulse chat server.
//
// Ported field-for-field from packages/cli/src/types.ts.
package wire

// ChatMessage is one message as returned by the chat server's history/poll
// endpoints.
type ChatMessage struct {
	ID      string `json:"id"`
	Role    string `json:"role"` // "user" | "agent"
	Ts      string `json:"ts"`
	Text    string `json:"text"`
	Channel string `json:"channel,omitempty"`
	Type    string `json:"type,omitempty"` // "alert" when set
}

// AgentInfo describes one registered agent.
type AgentInfo struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Color     string   `json:"color"`
	Nicknames []string `json:"nicknames,omitempty"`
	URLs      []string `json:"urls,omitempty"`
	Path      []string `json:"path,omitempty"`
}

// SubscribersInfo is the /api/chat/subscribers response shape.
type SubscribersInfo struct {
	Parlay *struct {
		Clients int `json:"clients,omitempty"`
	} `json:"parlay,omitempty"`
	Poll *struct {
		Count    int `json:"count,omitempty"`
		Channels []struct {
			Channel string `json:"channel"` // may be JSON null; empty string covers that case
			ID      string `json:"id,omitempty"`
			Name    string `json:"name,omitempty"`
		} `json:"channels,omitempty"`
	} `json:"poll,omitempty"`
	Registered *struct {
		Count  int         `json:"count,omitempty"`
		Agents []AgentInfo `json:"agents,omitempty"`
	} `json:"registered,omitempty"`
	PresenceBroadcasts int `json:"presence_broadcasts,omitempty"`
}
