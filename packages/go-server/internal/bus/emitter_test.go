package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGC writes a shell script standing in for the gc binary: each
// invocation appends one record to capturePath — every argv element on its
// own line, then the cwd and the GC_* env this package must pin/scrub,
// then an END marker. Records stay parseable even though payloads contain
// spaces and newlines are flattened by JSON encoding.
func fakeGC(t *testing.T, dir, capturePath string) string {
	t.Helper()
	script := "#!/bin/sh\n" +
		"{\n" +
		"  for a in \"$@\"; do printf 'arg:%s\\n' \"$a\"; done\n" +
		"  printf 'cwd:%s\\n' \"$(pwd)\"\n" +
		"  printf 'env_city_path:%s\\n' \"${GC_CITY_PATH-unset}\"\n" +
		"  printf 'env_city:%s\\n' \"${GC_CITY-unset}\"\n" +
		"  printf 'env_dir:%s\\n' \"${GC_DIR-unset}\"\n" +
		"  printf 'END\\n'\n" +
		"} >> \"" + capturePath + "\"\n"
	bin := filepath.Join(dir, "gc")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gc: %v", err)
	}
	return bin
}

// newTestCity creates a minimal city root (gc event emit only needs the
// directory to exist for cwd-pinning; New only checks city.toml).
func newTestCity(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "city.toml"), []byte("# test city\n"), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}
	// Resolve symlinks now (macOS TempDir lives under /var -> /private/var)
	// so cwd comparisons against the fake gc's $(pwd) are apples-to-apples.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve city dir: %v", err)
	}
	return resolved
}

// record is one parsed fake-gc invocation.
type record struct {
	args        []string
	cwd         string
	envCityPath string
	envCity     string
	envDir      string
}

func readCapture(t *testing.T, path string) []record {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	var out []record
	var cur record
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		switch {
		case line == "END":
			out = append(out, cur)
			cur = record{}
		case strings.HasPrefix(line, "arg:"):
			cur.args = append(cur.args, strings.TrimPrefix(line, "arg:"))
		case strings.HasPrefix(line, "cwd:"):
			cur.cwd = strings.TrimPrefix(line, "cwd:")
		case strings.HasPrefix(line, "env_city_path:"):
			cur.envCityPath = strings.TrimPrefix(line, "env_city_path:")
		case strings.HasPrefix(line, "env_city:"):
			cur.envCity = strings.TrimPrefix(line, "env_city:")
		case strings.HasPrefix(line, "env_dir:"):
			cur.envDir = strings.TrimPrefix(line, "env_dir:")
		}
	}
	return out
}

func TestEmitInvokesGCWithPrefixedTypeAndVerbatimPayload(t *testing.T) {
	city := newTestCity(t)
	binDir := t.TempDir()
	capture := filepath.Join(binDir, "capture.txt")
	e, err := New(Config{GCBin: fakeGC(t, binDir, capture), CityPath: city})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	payload := map[string]any{"tool": "Bash", "channel": "events-lift-1", "desc": "a b c"}
	e.Emit("tool_event", payload)
	e.Close() // drains the queue, so the capture is complete after this

	recs := readCapture(t, capture)
	if len(recs) != 1 {
		t.Fatalf("want 1 gc invocation, got %d", len(recs))
	}
	r := recs[0]
	want := []string{"event", "emit", "parlay.tool_event", "--actor", "parlay-server", "--payload"}
	if len(r.args) != len(want)+1 {
		t.Fatalf("argv length: want %d, got %d (%q)", len(want)+1, len(r.args), r.args)
	}
	for i, w := range want {
		if r.args[i] != w {
			t.Fatalf("argv[%d]: want %q, got %q", i, w, r.args[i])
		}
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(r.args[len(want)]), &got); err != nil {
		t.Fatalf("payload arg is not JSON: %v (%q)", err, r.args[len(want)])
	}
	if got["tool"] != "Bash" || got["channel"] != "events-lift-1" || got["desc"] != "a b c" {
		t.Fatalf("payload not verbatim: %v", got)
	}
}

func TestEmitPinsCwdAndCityEnvAndScrubsAmbientOverrides(t *testing.T) {
	// Ambient GC_* pointing anywhere else must never reach gc — the write
	// would land in someone's live city.
	t.Setenv("GC_CITY", "some-live-city")
	t.Setenv("GC_CITY_PATH", "/nonexistent/live/city")
	t.Setenv("GC_DIR", "/nonexistent/gcdir")

	city := newTestCity(t)
	binDir := t.TempDir()
	capture := filepath.Join(binDir, "capture.txt")
	e, err := New(Config{GCBin: fakeGC(t, binDir, capture), CityPath: city})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.Emit("message", map[string]string{"id": "m1"})
	e.Close()

	recs := readCapture(t, capture)
	if len(recs) != 1 {
		t.Fatalf("want 1 gc invocation, got %d", len(recs))
	}
	r := recs[0]
	if r.cwd != city {
		t.Errorf("cwd: want %q, got %q", city, r.cwd)
	}
	if r.envCityPath != city {
		t.Errorf("GC_CITY_PATH: want %q, got %q", city, r.envCityPath)
	}
	if r.envCity != "unset" {
		t.Errorf("GC_CITY should be scrubbed, got %q", r.envCity)
	}
	if r.envDir != "unset" {
		t.Errorf("GC_DIR should be scrubbed, got %q", r.envDir)
	}
}

func TestEmitDropsOversizedPayloadAndKeepsRunning(t *testing.T) {
	city := newTestCity(t)
	binDir := t.TempDir()
	capture := filepath.Join(binDir, "capture.txt")
	e, err := New(Config{GCBin: fakeGC(t, binDir, capture), CityPath: city})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.Emit("tool_event", map[string]string{"big": strings.Repeat("x", maxPayloadBytes)})
	e.Emit("tool_event", map[string]string{"small": "ok"})
	e.Close()

	recs := readCapture(t, capture)
	if len(recs) != 1 {
		t.Fatalf("want only the small event through, got %d invocations", len(recs))
	}
	if !strings.Contains(recs[0].args[len(recs[0].args)-1], "small") {
		t.Fatalf("surviving event is not the small one: %q", recs[0].args)
	}
}

func TestNewValidatesLoudly(t *testing.T) {
	city := newTestCity(t)
	binDir := t.TempDir()
	gc := fakeGC(t, binDir, filepath.Join(binDir, "capture.txt"))

	if _, err := New(Config{GCBin: "", CityPath: city}); err == nil {
		t.Error("empty GCBin: want error, got nil")
	}
	if _, err := New(Config{GCBin: filepath.Join(binDir, "no-such-gc"), CityPath: city}); err == nil {
		t.Error("missing gc binary: want error, got nil")
	}
	if _, err := New(Config{GCBin: gc, CityPath: t.TempDir()}); err == nil {
		t.Error("city without city.toml: want error, got nil")
	}
}

func TestNilEmitterAndPostCloseEmitAreSafe(t *testing.T) {
	var nilE *Emitter
	nilE.Emit("tool_event", "x") // must not panic
	nilE.Close()                 // must not panic

	city := newTestCity(t)
	binDir := t.TempDir()
	e, err := New(Config{GCBin: fakeGC(t, binDir, filepath.Join(binDir, "c.txt")), CityPath: city})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.Close()
	e.Emit("tool_event", "after close") // must not panic or block
}

func TestEmitUnmarshalablePayloadIsDroppedSilently(t *testing.T) {
	city := newTestCity(t)
	binDir := t.TempDir()
	capture := filepath.Join(binDir, "capture.txt")
	e, err := New(Config{GCBin: fakeGC(t, binDir, capture), CityPath: city})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.Emit("tool_event", func() {}) // funcs don't marshal
	e.Close()
	if recs := readCapture(t, capture); len(recs) != 0 {
		t.Fatalf("unmarshalable payload reached gc: %d invocations", len(recs))
	}
}
