package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

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
	mux.HandleFunc("/api/chat/declare-channel", handleDeclareChannel(st))
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

		// Both of these truncate or rewrite messages.jsonl and both return an
		// error saying whether that worked. Discarding it would report ok:true
		// with a plausible `removed` count while the history on disk is
		// untouched — so the next restart resurrects everything the caller was
		// told had been deleted.
		before := st.Messages.Len()
		var err error
		if req.Channel != "" {
			err = st.Messages.RemoveByChannel(req.Channel)
		} else {
			err = st.Messages.Clear()
		}
		if err != nil {
			writeAppError(w, "failed to clear history")
			return
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
	URL        string `json:"url"`
	OpenDrawer bool   `json:"open_drawer"`
	Device     string `json:"device"`
}

// navigateResponse is the response from POST /api/chat/navigate.
type navigateResponse struct {
	OK         bool   `json:"ok"`
	URL        string `json:"url"`
	OpenDrawer bool   `json:"open_drawer"`
	Clients    int    `json:"clients"`
	Device     string `json:"device,omitempty"`
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
			OK:         true,
			URL:        req.URL,
			OpenDrawer: req.OpenDrawer,
			Clients:    clients,
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

		// Truncate to 500 characters to match TS behavior. Cutting the byte
		// slice would split a multi-byte rune — an emoji or an accented
		// character straddling the boundary becomes a replacement character in
		// the stored history, and `len()` counts bytes, so a message of 500
		// non-ASCII characters would be cut well short of its real length.
		text := req.Text
		if utf8.RuneCountInString(text) > 500 {
			text = string([]rune(text)[:500])
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

// bundleVersionCache holds the mtime and version to avoid repeated file reads.
// The mutex is not optional: /api/chat/version is served concurrently, and
// without it two simultaneous requests race on these two fields and can pair
// one request's mtime with another's version — caching a stale version against
// a fresh mtime, which then never expires.
var bundleVersionCache struct {
	mu      sync.Mutex
	mtime   int64
	version string
}

// bundleVersionRe extracts PA_VERSION from the compiled client bundle.
// Compiled once at init rather than per request — MustCompile on a constant
// pattern either always works or panics at startup, which is where a bad
// pattern should surface.
var bundleVersionRe = regexp.MustCompile(`PA_VERSION\s*=\s*["']([^"']+)["']`)

// getBundleVersion reads the PA_VERSION from the compiled client bundle,
// caching by mtime to avoid repeated file reads. Every failure path returns
// "unknown" rather than an error: the panel treats an unknown version as
// "don't offer an update", which is the right behavior when the bundle is
// missing or unreadable.
func getBundleVersion() string {
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

	bundleVersionCache.mu.Lock()
	defer bundleVersionCache.mu.Unlock()
	if bundleVersionCache.mtime == mtime && bundleVersionCache.version != "" {
		return bundleVersionCache.version
	}

	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return "unknown"
	}

	// Extract PA_VERSION from the bundle using regex
	matches := bundleVersionRe.FindStringSubmatch(string(data))
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
func handleDeclareChannel(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		var req declareChannelRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		sessionID := strings.TrimSpace(req.SessionID)
		channel := strings.TrimSpace(req.Channel)
		if sessionID == "" || channel == "" {
			writeAppError(w, "session_id and channel required")
			return
		}

		// Declarations are sticky: the channel that comes back is the first
		// one ever declared for this session, which may differ from what was
		// just sent. Echo what is actually in effect, not the request — a
		// caller that re-declares under a different channel needs to see that
		// its declaration did not take.
		effective, err := st.Channels.Declare(sessionID, channel)
		if err != nil {
			writeAppError(w, "failed to declare channel")
			return
		}

		writeJSON(w, declareChannelResponse{
			OK:        true,
			SessionID: sessionID,
			Channel:   effective,
		})
	}
}
