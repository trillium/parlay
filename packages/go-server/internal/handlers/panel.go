package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"parlay/go-server/internal/store"
)

// registerPanel wires the panel-control routes onto mux. Call alongside
// Register and RegisterData in cmd/parlay-server/main.go.
func registerPanel(mux *http.ServeMux, st *store.Store, b *broker, hub *Hub) {
	mux.HandleFunc("/api/chat/clear", handleClear(st, hub))
	mux.HandleFunc("/api/chat/navigate", handleNavigate(hub))
	mux.HandleFunc("/api/chat/reload", handleReload(hub))
	mux.HandleFunc("/api/chat/device-cmd", handleDeviceCmd(hub))
	mux.HandleFunc("/api/chat/system", handleSystem(st, b, hub))
	mux.HandleFunc("/api/chat/version", handleVersion())
	mux.HandleFunc("/api/chat/declare-channel", handleDeclareChannel())
}

// clearRequest is the POST /api/chat/clear body.
type clearRequest struct {
	Channel string `json:"channel"`
}

// clearResponse is the response from POST /api/chat/clear.
type clearResponse struct {
	OK        bool `json:"ok"`
	Removed   int  `json:"removed"`
	Remaining int  `json:"remaining"`
}

// handleClear implements POST /api/chat/clear.
func handleClear(st *store.Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		var req clearRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		before := st.Messages.Len()
		if req.Channel != "" {
			st.Messages.RemoveByChannel(req.Channel)
		} else {
			st.Messages.Clear()
		}
		after := st.Messages.Len()

		hub.broadcast("reload", struct{}{})
		writeJSON(w, clearResponse{
			OK:        true,
			Removed:   before - after,
			Remaining: after,
		})
	}
}

// navigateRequest is the POST /api/chat/navigate body.
type navigateRequest struct {
	URL       string `json:"url"`
	OpenDrawer bool  `json:"open_drawer"`
	Device    string `json:"device"`
}

// navigateResponse is the response from POST /api/chat/navigate.
type navigateResponse struct {
	OK        bool   `json:"ok"`
	URL       string `json:"url"`
	OpenDrawer bool  `json:"open_drawer"`
	Clients   int    `json:"clients"`
	Device    string `json:"device,omitempty"`
}

// handleNavigate implements POST /api/chat/navigate.
func handleNavigate(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		var req navigateRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		if req.URL == "" {
			writeAppError(w, "url required")
			return
		}

		payload := map[string]interface{}{
			"url":        req.URL,
			"openDrawer": req.OpenDrawer,
		}
		clients := hub.broadcastToDevice(req.Device, "navigate", payload)

		resp := navigateResponse{
			OK:        true,
			URL:       req.URL,
			OpenDrawer: req.OpenDrawer,
			Clients:   clients,
		}
		if req.Device != "" {
			resp.Device = req.Device
		}
		writeJSON(w, resp)
	}
}

// reloadRequest is the POST /api/chat/reload body.
type reloadRequest struct {
	Device string `json:"device"`
}

// reloadResponse is the response from POST /api/chat/reload.
type reloadResponse struct {
	OK      bool   `json:"ok"`
	Clients int    `json:"clients"`
	Device  string `json:"device,omitempty"`
}

// handleReload implements POST /api/chat/reload.
func handleReload(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		var req reloadRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		clients := hub.broadcastToDevice(req.Device, "reload", struct{}{})

		resp := reloadResponse{
			OK:      true,
			Clients: clients,
		}
		if req.Device != "" {
			resp.Device = req.Device
		}
		writeJSON(w, resp)
	}
}

// deviceCmdRequest is the POST /api/chat/device-cmd body.
type deviceCmdRequest struct {
	Cmd    string                 `json:"cmd"`
	Args   map[string]interface{} `json:"args"`
	Device string                 `json:"device"`
}

// deviceCmdResponse is the response from POST /api/chat/device-cmd.
type deviceCmdResponse struct {
	OK   bool   `json:"ok"`
	Cmd  string `json:"cmd"`
	Sent int    `json:"sent"`
}

// handleDeviceCmd implements POST /api/chat/device-cmd.
func handleDeviceCmd(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		var req deviceCmdRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		if req.Cmd == "" {
			writeAppError(w, "cmd required")
			return
		}

		if req.Args == nil {
			req.Args = make(map[string]interface{})
		}

		payload := map[string]interface{}{
			"cmd":  req.Cmd,
			"args": req.Args,
		}
		sent := hub.broadcastToDevice(req.Device, "device_cmd", payload)

		writeJSON(w, deviceCmdResponse{
			OK:   true,
			Cmd:  req.Cmd,
			Sent: sent,
		})
	}
}

// systemRequest is the POST /api/chat/system body.
type systemRequest struct {
	Text   string `json:"text"`
	Source string `json:"source"`
}

// systemResponse is the response from POST /api/chat/system.
type systemResponse struct {
	OK bool   `json:"ok"`
	ID string `json:"id"`
}

// handleSystem implements POST /api/chat/system.
func handleSystem(st *store.Store, b *broker, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		var req systemRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		if req.Text == "" {
			writeAppError(w, "text required")
			return
		}

		// Truncate text to 500 chars to match TS behavior
		text := req.Text
		if len(text) > 500 {
			text = text[:500]
		}

		meta := map[string]interface{}{
			"type": "system_update",
		}
		if req.Source != "" {
			meta["source"] = req.Source
		}

		msg := store.ChatMessage{
			Role:    "agent",
			Text:    text,
			Channel: "system",
			Type:    "system_update",
			Meta:    meta,
		}

		stored, _, err := appendAndPublish(st, b, msg)
		if err != nil {
			writeAppError(w, "failed to store message")
			return
		}

		writeJSON(w, systemResponse{
			OK: true,
			ID: stored.ID,
		})
	}
}

// versionResponse is the response from GET /api/chat/version.
type versionResponse struct {
	Version string `json:"version"`
}

// bundleVersionCache holds the mtime and version to avoid repeated file reads
var bundleVersionCache struct {
	mtime   int64
	version string
}

// getBundleVersion reads the PA_VERSION from the compiled client bundle,
// caching by mtime to avoid repeated file reads.
func getBundleVersion() string {
	defer func() {
		if recover() != nil {
			// If anything panics, just return unknown
		}
	}()

	// Try to read from the same path the TS version reads
	home, err := os.UserHomeDir()
	if err != nil {
		return "unknown"
	}

	bundlePath := filepath.Join(home, "pulse-pages", "annotate", "pulse-agent.js")
	info, err := os.Stat(bundlePath)
	if err != nil {
		return "unknown"
	}

	mtime := info.ModTime().UnixMilli()
	if bundleVersionCache.mtime == mtime && bundleVersionCache.version != "" {
		return bundleVersionCache.version
	}

	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return "unknown"
	}

	// Extract PA_VERSION from the bundle using regex
	re := regexp.MustCompile(`PA_VERSION\s*=\s*["']([^"']+)["']`)
	matches := re.FindStringSubmatch(string(data))
	version := "unknown"
	if len(matches) > 1 {
		version = matches[1]
	}

	bundleVersionCache.mtime = mtime
	bundleVersionCache.version = version
	return version
}

// handleVersion implements GET /api/chat/version.
func handleVersion() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}

		version := getBundleVersion()
		writeJSON(w, versionResponse{
			Version: version,
		})
	}
}

// declareChannelRequest is the POST /api/chat/declare-channel body.
type declareChannelRequest struct {
	SessionID string `json:"session_id"`
	Channel   string `json:"channel"`
}

// declareChannelResponse is the response from POST /api/chat/declare-channel.
type declareChannelResponse struct {
	OK        bool   `json:"ok"`
	SessionID string `json:"session_id"`
	Channel   string `json:"channel"`
}

// handleDeclareChannel implements POST /api/chat/declare-channel.
func handleDeclareChannel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		var req declareChannelRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		if req.SessionID == "" || req.Channel == "" {
			writeAppError(w, "session_id and channel required")
			return
		}

		// declareChannel is a function from session-channel.ts in the TS server
		// We need to implement this in the Go server's store layer
		// For now, just return success
		// TODO: implement the actual channel declaration storage

		writeJSON(w, declareChannelResponse{
			OK:        true,
			SessionID: req.SessionID,
			Channel:   req.Channel,
		})
	}
}
