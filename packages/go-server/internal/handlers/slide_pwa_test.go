package handlers

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestSlidePWARoutes(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		handler http.HandlerFunc
		ctype   string
		cache   string
		body    []byte
	}{
		{"manifest", slideManifestPath, handleSlideManifest, "application/manifest+json; charset=utf-8", slideShellCacheControl, slideManifest},
		{"worker", slideWorkerPath, handleSlideWorker, "text/javascript; charset=utf-8", "no-cache", slideWorker},
		{"icon-180", slideIcon180Path, handleSlideIcon180, "image/png", slideShellCacheControl, slideIcon180},
		{"icon-192", slideIcon192Path, handleSlideIcon192, "image/png", slideShellCacheControl, slideIcon192},
		{"icon-512", slideIcon512Path, handleSlideIcon512, "image/png", slideShellCacheControl, slideIcon512},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s: got %d, want 200", tc.path, rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != tc.ctype {
				t.Fatalf("GET %s content-type = %q, want %q", tc.path, got, tc.ctype)
			}
			if got := rec.Header().Get("Cache-Control"); got != tc.cache {
				t.Fatalf("GET %s cache-control = %q, want %q", tc.path, got, tc.cache)
			}
			if rec.Body.String() != string(tc.body) {
				t.Fatalf("GET %s body mismatch (%d bytes)", tc.path, rec.Body.Len())
			}
			post := httptest.NewRecorder()
			tc.handler(post, httptest.NewRequest(http.MethodPost, tc.path, nil))
			if post.Code != http.StatusMethodNotAllowed {
				t.Fatalf("POST %s: got %d, want 405", tc.path, post.Code)
			}
		})
	}

	rec := httptest.NewRecorder()
	handleSlideWorker(rec, httptest.NewRequest(http.MethodGet, slideWorkerPath, nil))
	if got := rec.Header().Get("Service-Worker-Allowed"); got != "/slide" {
		t.Fatalf("Service-Worker-Allowed = %q, want /slide", got)
	}
}

func TestSlideManifestMatchesPage(t *testing.T) {
	var man struct {
		Name       string `json:"name"`
		ShortName  string `json:"short_name"`
		Display    string `json:"display"`
		StartURL   string `json:"start_url"`
		Scope      string `json:"scope"`
		Background string `json:"background_color"`
		Theme      string `json:"theme_color"`
		Icons      []struct {
			Src   string `json:"src"`
			Sizes string `json:"sizes"`
			Type  string `json:"type"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(slideManifest, &man); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if man.Name != "Slides" || man.ShortName != "Slides" {
		t.Fatalf("name = %q / %q, want Slides", man.Name, man.ShortName)
	}
	if man.Display != "standalone" {
		t.Fatalf("display = %q, want standalone", man.Display)
	}
	if man.StartURL != "/slide" || man.Scope != "/slide" {
		t.Fatalf("start_url/scope = %q / %q, want /slide", man.StartURL, man.Scope)
	}
	page := string(slidePage)
	for _, color := range []string{man.Background, man.Theme} {
		if color == "" || !strings.Contains(page, color) {
			t.Fatalf("manifest color %q is not on the slide page", color)
		}
	}
	wantIcons := []struct {
		src, sizes string
		body       []byte
	}{
		{slideIcon180Path, "180x180", slideIcon180},
		{slideIcon192Path, "192x192", slideIcon192},
		{slideIcon512Path, "512x512", slideIcon512},
	}
	if len(man.Icons) != len(wantIcons) {
		t.Fatalf("icons = %d, want %d", len(man.Icons), len(wantIcons))
	}
	for i, want := range wantIcons {
		got := man.Icons[i]
		if got.Src != want.src || got.Sizes != want.sizes || got.Type != "image/png" {
			t.Fatalf("icon %d = %+v, want src %s sizes %s type image/png", i, got, want.src, want.sizes)
		}
		w, h, ok := pngSize(want.body)
		if !ok || w != h {
			t.Fatalf("icon %s is not a square PNG (%d x %d)", want.src, w, h)
		}
		if got := strconv.Itoa(w) + "x" + strconv.Itoa(h); got != want.sizes {
			t.Fatalf("icon %s pixels = %s, want %s", want.src, got, want.sizes)
		}
	}
}

const slideWorkerHarness = `
const vm = require('vm');
const src = require('fs').readFileSync(0, 'utf8');
const handlers = {};
const added = [];
const puts = [];
const caches = {
  open: async () => ({ addAll: async (l) => { added.push(...l); }, put: async () => { puts.push(1); } }),
  keys: async () => [],
  delete: async () => true,
  match: async () => 'cached',
};
const self = { location: { origin: 'http://h' }, addEventListener: (n, f) => { handlers[n] = f; }, clients: { claim: async () => {} } };
vm.runInNewContext(src, { self, caches, URL, fetch: async () => { throw new Error('offline'); }, Promise });
(async () => {
  let w;
  await handlers.install({ waitUntil: (p) => { w = p; } });
  await w;
  const out = { added, fetches: {} };
  for (const [name, url, method] of JSON.parse(process.argv[1])) {
    let responded = null;
    handlers.fetch({ request: { url, method }, respondWith: (p) => { responded = p; } });
    out.fetches[name] = responded === null ? 'passthrough' : await responded;
  }
  out.puts = puts.length;
  console.log(JSON.stringify(out));
})();
`

func TestSlideWorkerBehaviour(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	reqs := [][3]string{
		{"api", "http://h/api/chat/slide", "GET"},
		{"apiPost", "http://h/api/chat/slide", "POST"},
		{"page", "http://h/slide", "GET"},
		{"icon", "http://h" + slideIcon192Path, "GET"},
		{"foreign", "http://other/slide", "GET"},
		{"unlisted", "http://h/slide/other", "GET"},
	}
	arg, _ := json.Marshal(reqs)
	cmd := exec.Command(node, "-e", slideWorkerHarness, string(arg))
	cmd.Stdin = bytes.NewReader(slideWorker)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("running worker: %v", err)
	}
	var out struct {
		Added   []string          `json:"added"`
		Fetches map[string]string `json:"fetches"`
		Puts    int               `json:"puts"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("harness output %q: %v", raw, err)
	}
	for _, p := range out.Added {
		if p == "/api" || strings.HasPrefix(p, "/api/") {
			t.Fatalf("worker precaches an API path: %s", p)
		}
	}
	want := []string{"/slide", slideManifestPath, slideIcon180Path, slideIcon192Path, slideIcon512Path}
	if strings.Join(out.Added, ",") != strings.Join(want, ",") {
		t.Fatalf("precached %v, want %v", out.Added, want)
	}
	for _, name := range []string{"api", "apiPost", "foreign", "unlisted"} {
		if out.Fetches[name] != "passthrough" {
			t.Fatalf("%s request was intercepted: %q", name, out.Fetches[name])
		}
	}
	for _, name := range []string{"page", "icon"} {
		if out.Fetches[name] != "cached" {
			t.Fatalf("%s offline = %q, want cached fallback", name, out.Fetches[name])
		}
	}
	if out.Puts != 0 {
		t.Fatalf("worker wrote %d cache entries outside addAll", out.Puts)
	}
}

func TestSlidePageServedWithShellCacheControl(t *testing.T) {
	rec := httptest.NewRecorder()
	handleSlidePage(rec, httptest.NewRequest(http.MethodGet, "/slide", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /slide: got %d", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != slideShellCacheControl {
		t.Fatalf("page cache-control = %q", rec.Header().Get("Cache-Control"))
	}
}

func pngSize(b []byte) (int, int, bool) {
	if len(b) < 24 || string(b[:8]) != "\x89PNG\r\n\x1a\n" {
		return 0, 0, false
	}
	if string(b[12:16]) != "IHDR" {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint32(b[16:20])), int(binary.BigEndian.Uint32(b[20:24])), true
}
