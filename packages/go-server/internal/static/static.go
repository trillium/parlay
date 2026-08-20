// Package static serves the built packages/client/dist bundle from the Go
// server. Mount it as a catch-all on the mux AFTER all /api/* routes so
// those are never shadowed.
//
// Routes served:
//
//	GET /                        → dist/index.html
//	GET /parlay-agent.js         → dist/parlay-agent.js  (same origin as the panel)
//	GET /annotate/<path>         → dist/<path>            (Pulse-compat alias)
//	GET /<anything-else>         → dist/index.html        (SPA fallback)
//
// The /annotate/ prefix matches the Pulse symlink convention
// (~/pulse-pages/annotate → packages/client), so any page that already
// loads <script src="/annotate/pulse-agent.js"> keeps working unchanged
// while this server is serving the panel.
package static

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Handler returns an http.Handler that serves the static bundle rooted at dir
// (typically the path to packages/client/dist). If dir is empty or does not
// exist, every request returns a 503 with a helpful message.
func Handler(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dir == "" {
			http.Error(w, "no assets directory configured (set --assets-dir or PARLAY_ASSETS_DIR)", http.StatusServiceUnavailable)
			return
		}
		if _, err := os.Stat(dir); err != nil {
			http.Error(w, "assets directory not found: "+dir, http.StatusServiceUnavailable)
			return
		}

		p := r.URL.Path

		// /annotate/<rest> → serve <rest> from the bundle directory, matching
		// the Pulse symlink convention so existing script tags work unchanged.
		if strings.HasPrefix(p, "/annotate/") {
			rest := strings.TrimPrefix(p, "/annotate")
			serveOrFallback(w, r, dir, rest)
			return
		}

		serveOrFallback(w, r, dir, p)
	})
}

// serveOrFallback serves the file at dir+path when it exists, or falls back
// to index.html for any path that would otherwise 404 (SPA routing).
func serveOrFallback(w http.ResponseWriter, r *http.Request, dir, path string) {
	candidate := filepath.Join(dir, filepath.FromSlash(path))

	// Prevent path traversal outside dir.
	rel, err := filepath.Rel(dir, candidate)
	if err != nil || strings.HasPrefix(rel, "..") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	info, err := os.Stat(candidate)
	if err == nil && !info.IsDir() {
		http.ServeFile(w, r, candidate)
		return
	}

	// SPA fallback: serve index.html for unknown paths so the panel loads on
	// any URL the user bookmarks or refreshes.
	index := filepath.Join(dir, "index.html")
	if _, err := os.Stat(index); err != nil {
		http.Error(w, "index.html not found in assets directory — run `bun build.ts` in packages/client", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, index)
}
