package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"parlay/go-server/internal/guard"
)

func TestClientEndpointDescribesServerAndOrigin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_ALLOWED_ORIGINS", "https://env.example")
	if err := os.WriteFile(filepath.Join(dir, guard.AllowedOriginsFileName), []byte("https://file.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guard.SetStateDir(dir)
	t.Cleanup(func() { guard.SetStateDir("") })

	mux := http.NewServeMux()
	RegisterClient(mux, ClientInfo{Listen: "127.0.0.1:4242", Exposure: "loopback", StateDir: dir})

	req := httptest.NewRequest(http.MethodGet, "/api/chat/client", nil)
	req.Header.Set("Origin", "https://file.example")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("the onboarding document must not send CORS headers")
	}
	var got clientResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ContractVersion != ClientContractVersion || got.Listen != "127.0.0.1:4242" || got.Exposure != "loopback" {
		t.Fatalf("bad header fields: %+v", got)
	}
	if len(got.Admitted.File) != 1 || got.Admitted.File[0] != "https://file.example" ||
		len(got.Admitted.Env) != 1 || got.Admitted.Env[0] != "https://env.example" {
		t.Fatalf("bad admitted: %+v", got.Admitted)
	}
	if got.YourOrigin != "https://file.example" || got.YourOriginState != "listed" {
		t.Fatalf("origin echo: %q %q", got.YourOrigin, got.YourOriginState)
	}
	if len(got.Contract) != 5 {
		t.Fatalf("contract has %d steps, want 5", len(got.Contract))
	}

	post := httptest.NewRecorder()
	mux.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/api/chat/client", nil))
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d, want 405", post.Code)
	}
}
