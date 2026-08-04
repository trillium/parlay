package handlers

import (
	"net/http"

	"parlay/go-server/internal/store"
)

type registerAgentRequest struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Color     string   `json:"color"`
	Nicknames []string `json:"nicknames"`
	Caps      any      `json:"caps"`
}

type registerAgentResponse struct {
	OK        bool     `json:"ok"`
	Nicknames []string `json:"nicknames,omitempty"`
}

// handleRegisterAgent implements POST /api/chat/register-agent — an
// idempotent upsert, delegated straight to RegistryStore.Upsert's own
// partial-update-merge semantics. hub is ticket C2's SSE fan-out: every
// successful upsert also broadcasts `agent_register` (the incremental,
// single-agent counterpart to the bulk `agents` event sent on /events
// connect).
func handleRegisterAgent(st *store.Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var req registerAgentRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.ID == "" {
			writeAppError(w, "id is required")
			return
		}
		merged, err := st.Registry.Upsert(store.AgentInfo{
			ID:        req.ID,
			Name:      req.Name,
			Color:     req.Color,
			Nicknames: req.Nicknames,
			Caps:      req.Caps,
		})
		if err != nil {
			writeAppError(w, err.Error())
			return
		}
		hub.broadcast(eventAgentRegister, merged)
		writeJSON(w, registerAgentResponse{OK: true, Nicknames: merged.Nicknames})
	}
}

type unregisterRequest struct {
	ID string `json:"id"`
}

// handleUnregister implements POST /api/chat/unregister. docs/api-contract.md
// documents this endpoint as "failing loud with a non-2xx status on an
// unknown/already-gone id" — the opposite HTTP convention from
// register-agent/reply/send/alert — so a missing id is a 400 and an unknown
// id is a 404, even though RegistryStore.Remove itself treats "unknown id"
// as an idempotent no-op at the storage layer (its own doc comment); this
// handler is what translates that store-level no-op into the contract's
// documented HTTP-level failure.
func handleUnregister(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var req unregisterRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.ID == "" {
			writeStatusError(w, http.StatusBadRequest, "id is required")
			return
		}
		removed, err := st.Registry.Remove(req.ID)
		if err != nil {
			writeStatusError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !removed {
			writeStatusError(w, http.StatusNotFound, "unknown agent id")
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}
}

// handleAgents implements GET /api/chat/agents.
func handleAgents(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		writeJSON(w, st.Registry.List())
	}
}

type subscribersPollChannel struct {
	Channel *string `json:"channel"`
	ID      string  `json:"id,omitempty"`
	Name    string  `json:"name,omitempty"`
}

type subscribersPresenceEntry struct {
	Channel  string  `json:"channel"`
	LastSeen *string `json:"lastSeen"`
}

type subscribersResponse struct {
	Parlay struct {
		Clients int `json:"clients"`
	} `json:"parlay"`
	Poll struct {
		Count    int                      `json:"count"`
		Channels []subscribersPollChannel `json:"channels"`
	} `json:"poll"`
	Registered struct {
		Count  int               `json:"count"`
		Agents []store.AgentInfo `json:"agents"`
	} `json:"registered"`
	Presence []subscribersPresenceEntry `json:"presence"`
}

// handleSubscribers implements GET /api/chat/subscribers, combining
// PresenceTracker.Snapshot with RegistryStore.List as store.go's own doc
// comment on Snapshot anticipates. `memory`, `history`, and
// `presence_broadcasts` are deliberately omitted — see the package doc
// comment.
func handleSubscribers(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		snap := st.Presence.Snapshot()
		agents := st.Registry.List()

		var resp subscribersResponse
		resp.Parlay.Clients = snap.PanelClients

		resp.Poll.Count = snap.PollCount
		resp.Poll.Channels = make([]subscribersPollChannel, 0, len(snap.PollChannels))
		for _, pc := range snap.PollChannels {
			entry := subscribersPollChannel{Channel: channelPtr(pc.Channel)}
			if a, ok := st.Registry.Get(pc.Channel); ok {
				entry.ID = a.ID
				entry.Name = a.Name
			}
			resp.Poll.Channels = append(resp.Poll.Channels, entry)
		}

		resp.Registered.Count = len(agents)
		resp.Registered.Agents = agents

		resp.Presence = make([]subscribersPresenceEntry, 0, len(snap.Presence))
		for _, p := range snap.Presence {
			ts := p.LastSeen
			resp.Presence = append(resp.Presence, subscribersPresenceEntry{Channel: p.Channel, LastSeen: &ts})
		}

		writeJSON(w, resp)
	}
}

// channelPtr maps the default channel ("") to JSON null, matching the
// contract's `channel: string | null` typing for poll.channels entries.
func channelPtr(channel string) *string {
	if channel == "" {
		return nil
	}
	c := channel
	return &c
}
