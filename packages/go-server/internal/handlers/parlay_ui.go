package handlers

import (
	"net/http"
	"os"
	"path/filepath"
)

// ── parlay-ui.js (port of parlay-ui.ts) ────────────────────────────────────
// Serves the client UI utility (__paPageId, __paRegisterInput, syntax
// highlight via data-lang). The TS server reads parlay-ui.js from alongside
// its source; that file is build-generated and not committed, so both servers
// degrade to a stub comment when it is absent — a missing UI bundle is not a
// server failure.

const uiJsStub = "// parlay-ui.js missing from server bundle\n"

func uiJsPath() string {
	wd, _ := os.Getwd()
	for _, cand := range []string{
		filepath.Join(wd, "..", "..", "packages", "server", "src", "parlay-ui.js"),
		filepath.Join(wd, "packages", "server", "src", "parlay-ui.js"),
	} {
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return ""
}

// handleParlayUi implements GET /api/chat/parlay-ui.js.
func handleParlayUi() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		js := uiJsStub
		if p := uiJsPath(); p != "" {
			if b, err := os.ReadFile(p); err == nil {
				js = string(b)
			}
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(js))
	}
}
