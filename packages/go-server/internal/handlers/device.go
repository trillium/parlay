package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"parlay/go-server/internal/store"
)

// ── Device-driving and read routes ──────────────────────────────────────────
//
// This file ports the TS server's remaining non-message routes that the panel
// calls: device-cmd (live debug without a page refresh), navigate (client-side
// workspace shell), reload (full page reload), clear (wipe history), pages
// (the pulse-pages index), and version (served-bundle freshness). Each
// broadcast below carries the exact payload shape the TS server sent, so the
// panel's existing SSE handlers see identical frames.

// deviceCmdPayload is the `device_cmd` event's documented {cmd, args} shape.
type deviceCmdPayload struct {
	Cmd  string `json:"cmd"`
	Args any    `json:"args"`
}

// handleDeviceCmd implements POST /api/chat/device-cmd. Body: {cmd, args?,
// device?}. Without a device it broadcasts to every connected panel; with one
// it targets only that panel (and reports the matched count).
func handleDeviceCmd(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			Cmd    string `json:"cmd"`
			Args   any    `json:"args"`
			Device string `json:"device"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if body.Cmd == "" {
			writeAppError(w, "cmd required")
			return
		}
		if body.Args == nil {
			body.Args = map[string]any{}
		}
		payload := deviceCmdPayload{Cmd: body.Cmd, Args: body.Args}
		sent := 0
		if body.Device != "" {
			sent = hub.broadcastToDevice(body.Device, eventDeviceCmd, payload)
		} else {
			hub.broadcast(eventDeviceCmd, payload)
		}
		writeJSON(w, map[string]any{"ok": true, "cmd": body.Cmd, "sent": sent})
	}
}

// handleNavigate implements POST /api/chat/navigate. Body: {url,
// open_drawer?, device?}. Navigates one device or all clients via the
// `navigate` SSE event.
func handleNavigate(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			URL        string `json:"url"`
			OpenDrawer bool   `json:"open_drawer"`
			Device     string `json:"device"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		url := strings.TrimSpace(body.URL)
		if url == "" {
			writeAppError(w, "url required")
			return
		}
		payload := map[string]any{"url": url, "openDrawer": body.OpenDrawer}
		sent := 0
		if body.Device != "" {
			sent = hub.broadcastToDevice(body.Device, eventNavigate, payload)
		} else {
			hub.broadcast(eventNavigate, payload)
		}
		writeJSON(w, map[string]any{"ok": true, "url": url, "openDrawer": body.OpenDrawer, "sent": sent})
	}
}

// handleReload implements POST /api/chat/reload. Body: {device?}. Broadcasts
// `reload` to one device or all clients; the client responds with
// location.reload().
func handleReload(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			Device string `json:"device"`
		}
		// Empty body is tolerated for back-compat: a bare POST /reload with no
		// JSON is a global reload.
		if r.Body != nil {
			_ = decodeJSON(w, r, &body)
		}
		sent := 0
		if body.Device != "" {
			sent = hub.broadcastToDevice(body.Device, eventReload, map[string]any{})
		} else {
			hub.broadcast(eventReload, map[string]any{})
		}
		writeJSON(w, map[string]any{"ok": true, "sent": sent})
	}
}

// handleClear implements POST /api/chat/clear. Body: {channel?}. Clears the
// whole retained history, or only one channel's messages, then broadcasts
// `reload` so every panel refetches. This is the one mutating route that also
// needs the message store's full reset, which store.ChatMessage does not
// otherwise expose.
func handleClear(st *store.Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			Channel string `json:"channel"`
		}
		// Empty body is tolerated: a bare POST /clear wipes everything.
		if r.Body != nil {
			_ = decodeJSON(w, r, &body)
		}
		channel := strings.TrimSpace(body.Channel)
		removed, remaining := st.Messages.Clear(channel)
		hub.broadcast(eventReload, map[string]any{})
		writeJSON(w, map[string]any{"ok": true, "removed": removed, "remaining": remaining})
	}
}

// ── Pages index (GET /api/chat/pages) ──────────────────────────────────────
// Serves the servable pages under ~/pulse-pages: every directory holding an
// index.html, with its <title>. Powers the panel's page-nav picker. Cached
// with a 30s TTL, matching the TS server's own cache.

const pagesRoot = "pulse-pages" // resolved against $HOME at request time

type pageEntry struct {
	Tag   string `json:"tag"`
	Title string `json:"title"`
}

var pagesCache struct {
	sync.Mutex
	at   time.Time
	list []pageEntry
}

func homePagesRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "pulse-pages"
	}
	return filepath.Join(home, "pulse-pages")
}

func listPages() []pageEntry {
	root := homePagesRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []pageEntry
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			idx := filepath.Join(root, e.Name(), "index.html")
			if fi, err := os.Stat(idx); err == nil && !fi.IsDir() {
				out = append(out, pageEntry{Tag: e.Name(), Title: titleOf(idx, e.Name())})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out
}

func titleOf(indexPath, fallback string) string {
	b, err := os.ReadFile(indexPath)
	if err != nil {
		return fallback
	}
	head := string(b)
	if len(head) > 4096 {
		head = head[:4096]
	}
	if i := strings.Index(strings.ToLower(head), "<title>"); i >= 0 {
		rest := head[i+len("<title>"):]
		if j := strings.Index(strings.ToLower(rest), "</title>"); j >= 0 {
			t := strings.Join(strings.Fields(rest[:j]), " ")
			if t != "" {
				return t
			}
		}
	}
	return fallback
}

const pagesTTL = 30 * time.Second

// handlePages implements GET /api/chat/pages. Returns {"pages": [{tag,title}]}.
func handlePages() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		pagesCache.Lock()
		if pagesCache.list == nil || time.Since(pagesCache.at) > pagesTTL {
			pagesCache.list = listPages()
			pagesCache.at = time.Now()
		}
		list := pagesCache.list
		pagesCache.Unlock()
		writeJSON(w, map[string]any{"pages": list})
	}
}

// ── Served-bundle version (GET /api/chat/version) ──────────────────────────
// Reads PA_VERSION from the compiled client bundle, mtime-cached, so PWA pages
// (which can live for days) compare against it on reconnect and self-reload
// when stale. Port of packages/server/src/bundle-version.ts.

type bundleVer struct {
	mtime   int64
	version string
}

var bundleCache struct {
	sync.Mutex
	v bundleVer
}

func bundleVersion() string {
	bundleCache.Lock()
	defer bundleCache.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return "unknown"
	}
	path := filepath.Join(home, "pulse-pages", "annotate", "pulse-agent.js")
	fi, err := os.Stat(path)
	if err != nil {
		return "unknown"
	}
	if bundleCache.v.version != "" && bundleCache.v.mtime == fi.ModTime().UnixNano() {
		return bundleCache.v.version
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}
	s := string(b)
	const marker = "PA_VERSION = "
	i := strings.Index(s, marker)
	ver := "unknown"
	if i >= 0 {
		rest := s[i+len(marker):]
		if rest[0] == '"' || rest[0] == '\'' {
			q := rest[0]
			if j := strings.IndexByte(rest[1:], q); j >= 0 {
				ver = rest[1 : 1+j]
			}
		}
	}
	bundleCache.v = bundleVer{mtime: fi.ModTime().UnixNano(), version: ver}
	return ver
}

// handleVersion implements GET /api/chat/version. Returns {version}.
func handleVersion() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		writeJSON(w, map[string]string{"version": bundleVersion()})
	}
}
