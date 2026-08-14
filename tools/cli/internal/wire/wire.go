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

// CommandInvocation is one live-command record from GET /api/chat/commands
// and from the `commands` / `command_update` SSE events. Field-for-field the
// server's store.CommandInvocation — see docs/live-commands.md.
//
// The server never sends a flag's VALUE, a positional argument, or raw argv:
// Flags carries flag names only. Nothing here is free-form text.
type CommandInvocation struct {
	ID         string   `json:"id"`
	Verb       string   `json:"verb"`
	Agent      string   `json:"agent,omitempty"`
	Channel    string   `json:"channel,omitempty"`
	Flags      []string `json:"flags,omitempty"`
	PID        int      `json:"pid,omitempty"`
	State      string   `json:"state"`
	StartedAt  string   `json:"startedAt"`
	UpdatedAt  string   `json:"updatedAt"`
	EndedAt    string   `json:"endedAt,omitempty"`
	ExitCode   *int     `json:"exitCode,omitempty"`
	Outcome    string   `json:"outcome,omitempty"`
	DurationMs int64    `json:"durationMs"`
}

// CommandsResponse is the GET /api/chat/commands response shape.
type CommandsResponse struct {
	OK           bool                `json:"ok"`
	Now          string              `json:"now"`
	Running      int                 `json:"running"`
	StaleAfterMs int64               `json:"staleAfterMs"`
	Commands     []CommandInvocation `json:"commands"`
}
