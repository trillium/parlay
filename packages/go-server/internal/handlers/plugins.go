package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"parlay/go-server/internal/store"
)

// ── Plugins, system, declare-channel, DELETE agents/:id ─────────────────────
//
// The remaining panel-facing routes the TS server served and the Go rewrite
// must cover for a full flip. Plugins is the manifest + the cursorless RPC
// bridge; system and declare-channel are the hook/agent announce + channel
// mapping routes; DELETE /api/chat/agents/:id is the REST alias for unregister.

// pluginManifest is the server-side plugin registry the panel loads. `speak`
// is listed first because it wires the global speech hooks; `cursorless` is
// the Talon-side editor RPC bridge. The client halves load from
// /annotate/plugins/<id>.js.
type pluginManifest struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	MinPanel       string `json:"minPanel,omitempty"`
	Description    string `json:"description"`
	DefaultEnabled bool   `json:"defaultEnabled"`
}

var pluginManifests = []pluginManifest{
	{ID: "speak", Version: "1.0.0", MinPanel: "3.7.0", Description: "Kokoro speech playback, readiness dots, pronunciation corrector", DefaultEnabled: true},
	{ID: "cursorless", Version: "1.0.0", MinPanel: "3.7.0", Description: "Talon-side editor RPC bridge", DefaultEnabled: true},
}

// handlePlugins implements GET /api/chat/plugins — returns the manifest array.
func handlePlugins() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		writeJSON(w, pluginManifests)
	}
}

// ── Cursorless RPC bridge ───────────────────────────────────────────────────
// Talon-side Python POSTs an editor op; this relays it to the panel over SSE
// (event `cursorless_rpc`), the panel applies it and POSTs the result back;
// this returns it to Talon. Same waiter pattern as chat polling, 2.5s timeout.

type rpcWaiter struct {
	resolve func(result any)
	timer   *time.Timer
}

var cursorlessWaiters struct {
	sync.Mutex
	m map[string]rpcWaiter
}

func init() { cursorlessWaiters.m = make(map[string]rpcWaiter) }

func newRPCID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// handleCursorlessRPC implements POST /api/chat/plugin/cursorless/rpc. Body:
// {op, args?, device?}. Relays to the panel and blocks up to 2.5s for the
// response.
func handleCursorlessRPC(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			Op     string `json:"op"`
			Args   any    `json:"args"`
			Device string `json:"device"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if body.Op == "" {
			writeJSON(w, map[string]any{"ok": false, "error": "op required"})
			return
		}

		rpcID := newRPCID()
		resultCh := make(chan any, 1)
		timer := time.AfterFunc(2500*time.Millisecond, func() {
			cursorlessWaiters.Lock()
			delete(cursorlessWaiters.m, rpcID)
			cursorlessWaiters.Unlock()
			resultCh <- map[string]any{"ok": false, "error": "panel did not respond (2.5s)"}
		})
		cursorlessWaiters.Lock()
		cursorlessWaiters.m[rpcID] = rpcWaiter{
			resolve: func(result any) {
				timer.Stop()
				resultCh <- map[string]any{"ok": true, "result": result}
			},
			timer: timer,
		}
		cursorlessWaiters.Unlock()

		payload := map[string]any{"rpcId": rpcID, "op": body.Op, "args": body.Args}
		delivered := 0
		if body.Device != "" {
			delivered = hub.broadcastToDevice(body.Device, "cursorless_rpc", payload)
		} else {
			hub.broadcast("cursorless_rpc", payload)
			delivered = 1
		}
		if body.Device != "" && delivered == 0 {
			timer.Stop()
			cursorlessWaiters.Lock()
			delete(cursorlessWaiters.m, rpcID)
			cursorlessWaiters.Unlock()
			writeJSON(w, map[string]any{"ok": false, "error": "no client for device " + body.Device})
			return
		}

		select {
		case res := <-resultCh:
			writeJSON(w, res)
		case <-r.Context().Done():
			timer.Stop()
			cursorlessWaiters.Lock()
			delete(cursorlessWaiters.m, rpcID)
			cursorlessWaiters.Unlock()
		}
	}
}

// handleCursorlessResponse implements POST /api/chat/plugin/cursorless/response.
// Body: {rpcId, result}. Resolves the waiting RPC, if any.
func handleCursorlessResponse() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			RPCID  string `json:"rpcId"`
			Result any    `json:"result"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		cursorlessWaiters.Lock()
		waiter, ok := cursorlessWaiters.m[body.RPCID]
		if ok {
			delete(cursorlessWaiters.m, body.RPCID)
		}
		cursorlessWaiters.Unlock()
		if ok {
			waiter.resolve(body.Result)
		}
		writeJSON(w, map[string]any{"ok": ok})
	}
}

// handleSystem implements POST /api/chat/system — hooks and other system
// components announce themselves as a muted system line in every tab.
func handleSystem(st *store.Store, b *broker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			Text   string `json:"text"`
			Source string `json:"source"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if body.Text == "" {
			writeAppError(w, "text required")
			return
		}
		msg := store.ChatMessage{
			Role:    "agent",
			Text:    truncate(body.Text, 500),
			Channel: "system",
			Type:    "system_update",
		}
		if body.Source != "" {
			msg.Source = truncate(body.Source, 60)
		}
		stored, _, err := appendAndPublish(st, b, msg)
		if err != nil {
			writeAppError(w, err.Error())
			return
		}
		writeJSON(w, okIDResponse{OK: true, ID: stored.ID})
	}
}

// handleDeclareChannel implements POST /api/chat/declare-channel — an agent
// declares its session→channel mapping explicitly. Written to the primary JSON
// file; env/tool-activity remains the fallback.
func handleDeclareChannel(sc *sessionChannels) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			SessionID string `json:"session_id"`
			Channel   string `json:"channel"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		body.SessionID = strings.TrimSpace(body.SessionID)
		body.Channel = strings.TrimSpace(body.Channel)
		if body.SessionID == "" || body.Channel == "" {
			writeAppError(w, "session_id and channel required")
			return
		}
		sc.declareChannel(body.SessionID, body.Channel)
		writeJSON(w, map[string]any{"ok": true, "session_id": body.SessionID, "channel": body.Channel})
	}
}

// handleDeleteAgent implements DELETE /api/chat/agents/:id — the REST alias for
// unregister, same fail-loud contract (404 on unknown id).
func handleDeleteAgent(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			methodNotAllowed(w, http.MethodDelete)
			return
		}
		path := r.URL.Path
		if !strings.HasPrefix(path, "/api/chat/agents/") {
			methodNotAllowed(w, http.MethodDelete)
			return
		}
		id := strings.TrimSpace(path[len("/api/chat/agents/"):])
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
			writeStatusError(w, http.StatusNotFound, "unknown agent")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "id": id})
	}
}
