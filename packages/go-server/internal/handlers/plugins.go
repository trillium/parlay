package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Plugin manifest entry
type PluginManifest struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	MinPanel       string `json:"minPanel"`
	Description    string `json:"description"`
	DefaultEnabled bool   `json:"defaultEnabled"`
}

// RPC waiter for plugin responses
type rpcWaiter struct {
	resolve func(result interface{})
	timer   *time.Timer
}

// Plugin registry and RPC state
var (
	pluginManifests []PluginManifest
	rpcWaiters      = make(map[string]*rpcWaiter)
	rpcWaitersMu    sync.Mutex
	pluginHub       *Hub // Will be set during RegisterPlugins

	// Plugin manifests - speak is always first, then other plugins
	pluginList = []PluginManifest{
		{
			ID:             "speak",
			Version:        "1.0.0",
			MinPanel:       "3.7.0",
			Description:    "Kokoro speech playback, readiness dots, pronunciation corrector",
			DefaultEnabled: true,
		},
		{
			ID:             "cursorless",
			Version:        "0.1.0",
			MinPanel:       "3.6.0",
			Description:    "Cursorless voice editing on the Parlay input (desktop, via Talon)",
			DefaultEnabled: true,
		},
	}
)

func init() {
	pluginManifests = pluginList
}

// handlePlugins handles GET /api/chat/plugins
func handlePlugins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pluginManifests)
}

// makePluginRouteHandler dispatches plugin-specific requests
func makePluginRouteHandler(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Cursorless RPC routes
		if r.Method == http.MethodPost && path == "/api/chat/plugin/cursorless/rpc" {
			makeCursorlessRPCHandler(hub)(w, r)
			return
		}

		if r.Method == http.MethodPost && path == "/api/chat/plugin/cursorless/response" {
			handleCursorlessResponse(w, r)
			return
		}

		http.NotFound(w, r)
	}
}

// makeCursorlessRPCHandler creates a handler for Talon-side RPC requests
func makeCursorlessRPCHandler(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var body struct {
			Op     string        `json:"op"`
			Args   interface{}   `json:"args"`
			Device string        `json:"device"`
		}

		if !decodeJSON(w, r, &body) {
			return
		}

		if body.Op == "" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":    false,
				"error": "op required",
			})
			return
		}

		// Generate random RPC ID (16 bytes = 32 hex chars, similar to UUID)
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":    false,
				"error": "failed to generate RPC ID",
			})
			return
		}
		rpcID := hex.EncodeToString(b)

		// Create response channel and timer
		responseChan := make(chan interface{}, 1)
		timer := time.AfterFunc(2500*time.Millisecond, func() {
			rpcWaitersMu.Lock()
			delete(rpcWaiters, rpcID)
			rpcWaitersMu.Unlock()

			select {
			case responseChan <- map[string]interface{}{"ok": false, "error": "panel did not respond (2.5s)"}:
			default:
			}
		})

		rpcWaitersMu.Lock()
		rpcWaiters[rpcID] = &rpcWaiter{
			resolve: func(result interface{}) {
				timer.Stop()
				select {
				case responseChan <- map[string]interface{}{"ok": true, "result": result}:
				default:
				}
			},
			timer: timer,
		}
		rpcWaitersMu.Unlock()

		// Broadcast to clients
		payload := map[string]interface{}{
			"rpcId": rpcID,
			"op":    body.Op,
			"args":  body.Args,
		}

		if hub != nil {
			hub.broadcast("cursorless_rpc", payload)
		}

		// Wait for response or timeout
		result := <-responseChan
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// handleCursorlessResponse handles panel responses to RPC requests
func handleCursorlessResponse(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var body struct {
		RPCID  string      `json:"rpcId"`
		Result interface{} `json:"result"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	rpcWaitersMu.Lock()
	waiter := rpcWaiters[body.RPCID]
	if waiter != nil {
		delete(rpcWaiters, body.RPCID)
		waiter.timer.Stop()
	}
	rpcWaitersMu.Unlock()

	ok := waiter != nil
	if ok && waiter != nil {
		waiter.resolve(body.Result)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": ok,
	})
}

// RegisterPages wires the pages route and starts watching for changes.
func RegisterPages(mux *http.ServeMux, hub *Hub) {
	mux.HandleFunc("/api/chat/pages", handlePages)

	// Start watching for page changes
	watchPages(hub)
}

// RegisterPlugins wires the plugins and plugin RPC routes.
func RegisterPlugins(mux *http.ServeMux, hub *Hub) {
	mux.HandleFunc("/api/chat/plugins", handlePlugins)
	mux.HandleFunc("/api/chat/plugin/", makePluginRouteHandler(hub))
}
