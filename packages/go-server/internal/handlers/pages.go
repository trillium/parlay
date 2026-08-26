package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Pages handler for GET /api/chat/pages
// Lists servable pages from ~/pulse-pages/: every directory holding an index.html,
// with its <title> for fuzzy search. Powers the panel's page-nav picker.
// Cached with a 30s TTL.

type PageEntry struct {
	Tag   string `json:"tag"`
	Title string `json:"title"`
}

type pagesCache struct {
	mu    sync.Mutex
	at    time.Time
	pages []PageEntry
}

const pagesCacheTTL = 30 * time.Second

var (
	pagesRoot  = ""
	pagesStore = &pagesCache{}
	titleRegex = regexp.MustCompile(`(?i)<title>([^<]*)</title>`)
)

func initPagesRoot() string {
	if pagesRoot != "" {
		return pagesRoot
	}
	if home, err := os.UserHomeDir(); err == nil {
		pagesRoot = filepath.Join(home, "pulse-pages")
		return pagesRoot
	}
	return "pulse-pages"
}

func titleOf(indexPath string, fallback string) string {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return fallback
	}

	// Look at first 4096 bytes
	head := string(data)
	if len(head) > 4096 {
		head = head[:4096]
	}

	matches := titleRegex.FindStringSubmatch(head)
	if len(matches) > 1 {
		title := matches[1]
		// Normalize whitespace
		spaceRegex := regexp.MustCompile(`\s+`)
		title = spaceRegex.ReplaceAllString(title, " ")
		title = strings.TrimSpace(title)
		if title != "" {
			return title
		}
	}

	return fallback
}

func listPages() []PageEntry {
	root := initPagesRoot()
	var out []PageEntry

	entries, err := os.ReadDir(root)
	if err != nil {
		// pulse-pages may not exist at startup
		return out
	}

	for _, entry := range entries {
		if entry.Name()[0] == '.' {
			continue
		}

		if !entry.IsDir() {
			continue
		}

		indexPath := filepath.Join(root, entry.Name(), "index.html")
		info, err := os.Stat(indexPath)
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}

		out = append(out, PageEntry{
			Tag:   entry.Name(),
			Title: titleOf(indexPath, entry.Name()),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Tag < out[j].Tag
	})

	return out
}

func handlePages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}

	pagesStore.mu.Lock()
	defer pagesStore.mu.Unlock()

	// Check cache TTL
	if time.Since(pagesStore.at) > pagesCacheTTL {
		pagesStore.pages = listPages()
		pagesStore.at = time.Now()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]PageEntry{
		"pages": pagesStore.pages,
	})
}

// watchPages watches ~/pulse-pages/ for changes and broadcasts patches to all clients.
// This is called during initialization to set up the watcher.
func watchPages(hub *Hub) {
	// Simple file system watcher - we'll poll for changes
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		var prevPages []PageEntry
		var debounce *time.Timer

		for range ticker.C {
			if debounce != nil {
				continue // Still in debounce period
			}

			fresh := listPages()

			// Check if anything changed
			if !pagesChanged(prevPages, fresh) {
				continue
			}

			// Compute diff
			prevTags := make(map[string]bool)
			for _, p := range prevPages {
				prevTags[p.Tag] = true
			}

			freshTags := make(map[string]bool)
			for _, p := range fresh {
				freshTags[p.Tag] = true
			}

			var added []PageEntry
			for _, p := range fresh {
				if !prevTags[p.Tag] {
					added = append(added, p)
				}
			}

			var removed []string
			for _, p := range prevPages {
				if !freshTags[p.Tag] {
					removed = append(removed, p.Tag)
				}
			}

			if len(added) > 0 || len(removed) > 0 {
				pagesStore.mu.Lock()
				pagesStore.pages = fresh
				pagesStore.at = time.Now()
				pagesStore.mu.Unlock()

				// Broadcast to all clients
				payload := map[string]interface{}{
					"added":   added,
					"removed": removed,
				}
				hub.broadcast("pages_patch", payload)
			}

			prevPages = fresh

			// Set debounce timer
			debounce = time.AfterFunc(500*time.Millisecond, func() {
				debounce = nil
			})
		}
	}()
}

func pagesChanged(prev, fresh []PageEntry) bool {
	if len(prev) != len(fresh) {
		return true
	}

	for i, p := range prev {
		if i >= len(fresh) || p != fresh[i] {
			return true
		}
	}

	return false
}
