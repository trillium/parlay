package handlers

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestSlidePageIsInstallable(t *testing.T) {
	rec := httptest.NewRecorder()
	handleSlidePage(rec, httptest.NewRequest(http.MethodGet, "/slide", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /slide: got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`name="apple-mobile-web-app-capable" content="yes"`,
		`name="apple-mobile-web-app-status-bar-style" content="default"`,
		`name="apple-mobile-web-app-title" content="Slides"`,
		`rel="manifest" href="/slide/manifest.webmanifest"`,
		`rel="apple-touch-icon" sizes="180x180" href="/slide/icon-180.png"`,
		`maximum-scale=1`,
		`user-scalable=no`,
		`touch-action:manipulation`,
		`min-height:44px`,
		`serviceWorker.register('/slide/sw.js', { scope: '/slide' })`,
		`content="#ffffff" media="(prefers-color-scheme: light)"`,
		`content="#0d1117" media="(prefers-color-scheme: dark)"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("slide page missing %q", want)
		}
	}
	if rec.Header().Get("Cache-Control") != slideShellCacheControl {
		t.Fatalf("page cache-control = %q", rec.Header().Get("Cache-Control"))
	}
}

func TestSlideWorkerNeverCachesAPI(t *testing.T) {
	src := string(slideWorker)
	shell := shellPaths(t, src)
	want := []string{"/slide", slideManifestPath, slideIcon180Path, slideIcon192Path, slideIcon512Path}
	if strings.Join(shell, ",") != strings.Join(want, ",") {
		t.Fatalf("SHELL = %v, want %v", shell, want)
	}
	for _, p := range shell {
		if p == "/api" || strings.HasPrefix(p, "/api/") {
			t.Fatalf("SHELL caches an API path: %s", p)
		}
	}
	if strings.Contains(src, ".put(") {
		t.Fatal("service worker writes cache entries outside addAll(SHELL)")
	}
	fetch := strings.Index(src, "addEventListener('fetch'")
	if fetch < 0 {
		t.Fatal("service worker has no fetch handler")
	}
	rest := src[fetch:]
	api := strings.Index(rest, "isAPI(url.pathname)")
	respond := strings.Index(rest, "respondWith")
	if api < 0 || respond < 0 || api > respond {
		t.Fatal("fetch handler must refuse /api before respondWith")
	}
	if !strings.Contains(src, "pathname.indexOf('/api/')") {
		t.Fatal("service worker does not match /api/ paths")
	}
}

func shellPaths(t *testing.T, src string) []string {
	t.Helper()
	start := strings.Index(src, "var SHELL = [")
	if start < 0 {
		t.Fatal("SHELL list missing")
	}
	rest := src[start:]
	end := strings.Index(rest, "]")
	if end < 0 {
		t.Fatal("SHELL list unclosed")
	}
	var out []string
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "'") {
			continue
		}
		path, _, ok := strings.Cut(strings.TrimPrefix(line, "'"), "'")
		if !ok {
			t.Fatalf("bad SHELL entry %q", line)
		}
		out = append(out, path)
	}
	return out
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
