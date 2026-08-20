package static

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
