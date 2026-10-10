package guard

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func withAdmitFile(t *testing.T, body string) string {
	t.Helper()
	t.Setenv("PARLAY_ALLOWED_ORIGINS", "")
	dir := t.TempDir()
	SetStateDir(dir)
	t.Cleanup(func() { SetStateDir("") })
	if body != "" {
		writeAdmit(t, dir, body)
	}
	return dir
}

func writeAdmit(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, AllowedOriginsFileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Defeat coarse mtime granularity between two writes in one test.
	future := time.Now().Add(time.Duration(len(body)+1) * time.Second)
	_ = os.Chtimes(path, future, future)
}

func TestAdmitFileAdmitsOriginWithoutRestart(t *testing.T) {
	dir := withAdmitFile(t, "")
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://example.invalid:4242/api/chat/send", nil)
		r.Host = "example.invalid:4242"
		r.Header.Set("Origin", "https://client.example.com")
		return r
	}
	if OriginAllowed(req()) {
		t.Fatal("origin must be refused before it is admitted")
	}
	writeAdmit(t, dir, "# herdr web\nhttps://client.example.com\n")
	if !OriginAllowed(req()) {
		t.Fatal("origin must be admitted once the file lists it, with no restart")
	}
	writeAdmit(t, dir, "# revoked\n")
	if OriginAllowed(req()) {
		t.Fatal("origin must be refused again after the file drops it")
	}
}

func TestAdmitFileMergesWithEnvAndIgnoresComments(t *testing.T) {
	dir := withAdmitFile(t, "https://a.example # trailing\n\n  https://b.example,https://c.example\n")
	t.Setenv("PARLAY_ALLOWED_ORIGINS", "https://env.example")
	got := AllowedOriginList()
	want := []string{"https://env.example", "https://a.example", "https://b.example", "https://c.example"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	_ = dir
}

func TestMissingAdmitFileAdmitsNothing(t *testing.T) {
	withAdmitFile(t, "")
	if got := FileOrigins(); len(got) != 0 {
		t.Fatalf("missing file must admit nothing, got %v", got)
	}
}

func TestClassifyOrigin(t *testing.T) {
	withAdmitFile(t, "https://listed.example\n")
	cases := map[string]string{
		"https://listed.example":     "listed",
		"http://localhost:8787":      "local",
		"http://192.168.1.9:8787":    "local",
		"http://100.100.1.1:8787":    "network",
		"http://box.tail1234.ts.net": "network",
		"https://evil.example.com":   "refused",
		"null":                       "refused",
		"http://srv:4242":            "same-origin",
	}
	for origin, want := range cases {
		if got := ClassifyOrigin(origin, "srv:4242"); got != want {
			t.Errorf("ClassifyOrigin(%q) = %q, want %q", origin, got, want)
		}
	}
	t.Setenv("PARLAY_ALLOWED_ORIGINS", "*")
	if got := ClassifyOrigin("https://anything.example", ""); got != "wildcard" {
		t.Errorf("wildcard: got %q", got)
	}
}
