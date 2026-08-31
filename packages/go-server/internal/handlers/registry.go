package handlers

import (
	"net/http"
	"strings"

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

type unregisterResponse struct {
	OK bool   `json:"ok"`
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
//
// A successful removal broadcasts `agent_unregister {id}` (the incremental
// counterpart to agent_register, same as the TS side's unregisterAgent) and
// answers `{ok, id}` — divergence 9's bare `{ok}` converged to the TS shape.
func handleUnregister(st *store.Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var req unregisterRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		removeAgent(w, st, hub, req.ID)
	}
}

// handleDeleteAgent implements DELETE /api/chat/agents/{id} — the REST alias
// of POST /api/chat/unregister (same removal, id from the trailing URL path
// instead of a JSON body) with the same status-error convention. The TS side
// URL-decodes and trims the trailing segment before the lookup; r.URL.Path
// arrives percent-decoded, so only the trim needs mirroring here. The
// /api/chat/agents/ subtree is guarded (internal/guard.guardedPrefixes), so
// this handler lands inside the boundary that pre-landed for it.
func handleDeleteAgent(st *store.Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			methodNotAllowed(w, http.MethodDelete)
			return
		}
		id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/chat/agents/"))
		removeAgent(w, st, hub, id)
	}
}

// removeAgent is the shared removal path behind unregister and its REST
// alias: 400 on an empty id, 404 on an unknown one, and on success an
// agent_unregister broadcast plus the contract's `{ok, id}` body.
func removeAgent(w http.ResponseWriter, st *store.Store, hub *Hub, id string) {
	if id == "" {
		writeStatusError(w, http.StatusBadRequest, "id required")
		return
	}
	removed, err := st.Registry.Remove(id)
	if err != nil {
		writeStatusError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !removed {
		writeStatusError(w, http.StatusNotFound, "unknown agent id")
		return
	}
	hub.broadcast(eventAgentUnregister, map[string]string{"id": id})
	writeJSON(w, unregisterResponse{OK: true, ID: id})
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
