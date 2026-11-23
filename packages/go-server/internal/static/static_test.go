package static

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerServesIndexHTML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>ok</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("GET / want 200 got %d", rr.Code)
	}
	if got := rr.Body.String(); got != "<html>ok</html>" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestHandlerServesStaticFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644)
	os.WriteFile(filepath.Join(dir, "parlay-agent.js"), []byte("// js"), 0o644)

	h := Handler(dir)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/parlay-agent.js", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /parlay-agent.js want 200 got %d", rr.Code)
	}
}

func TestHandlerAnnotatePrefixAlias(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644)
	os.WriteFile(filepath.Join(dir, "pulse-agent.js"), []byte("// pulse"), 0o644)

	h := Handler(dir)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/annotate/pulse-agent.js", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /annotate/pulse-agent.js want 200 got %d", rr.Code)
	}
}

func TestHandlerSPAFallback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>spa</html>"), 0o644)

	h := Handler(dir)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/some/unknown/path", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /unknown want 200 (SPA fallback) got %d", rr.Code)
	}
	if got := rr.Body.String(); got != "<html>spa</html>" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestHandlerNoDir(t *testing.T) {
	h := Handler("")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty dir want 503 got %d", rr.Code)
	}
}

// A newcomer's first server start hits the missing-bundle path before they
// have built the panel. The body has to say how to build it, not just echo a
// resolved path they have never heard of.
func TestHandlerMissingDirExplainsHowToBuildThePanel(t *testing.T) {
	h := Handler(filepath.Join(t.TempDir(), "nope"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing dir want 503 got %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"bun run build", "-assets-dir", "PARLAY_ASSETS_DIR"} {
		if !strings.Contains(body, want) {
			t.Fatalf("503 body missing %q: %q", want, body)
		}
	}
}

func TestHandlerPathTraversalBlocked(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644)

	h := Handler(dir)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/../../etc/passwd", nil))

	if rr.Code == http.StatusOK {
		t.Fatal("path traversal should not return 200")
	}
}

// An unrouted /api path must read as a 404, not as the panel's index.html with
// a 200 — callers treat 404 as "this server does not support that".
func TestHandlerNeverFallsBackToIndexUnderAPI(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>shell</html>"), 0o644)

	h := Handler(dir)
	for _, p := range []string{"/api/chat/debug-log", "/api/debug/input-timing", "/api/lavish/claim", "/api/", "/api"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("GET %s want 404 got %d", p, rr.Code)
		}
		if strings.Contains(rr.Body.String(), "shell") {
			t.Errorf("GET %s served the SPA shell: %q", p, rr.Body.String())
		}
	}

	// Non-API paths still fall back.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/some/bookmarked/route", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "shell") {
		t.Errorf("SPA fallback broke: %d %q", rr.Code, rr.Body.String())
	}
	// A path that merely starts with the letters "api" is not under /api/.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/apiary", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("GET /apiary want SPA fallback 200 got %d", rr.Code)
	}
}
