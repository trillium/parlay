package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

func TestCityScaffoldWritesAndReports(t *testing.T) {
	state := testsupport.TempStateHome(t)

	out := captureStdout(t, func() { CityScaffold(nil) })
	dir := filepath.Join(state, "gascity", "city")
	if !strings.Contains(out, "city scaffold reconciled at "+dir) {
		t.Errorf("CityScaffold() output = %q, want the scaffold dir named", out)
	}
	if !strings.Contains(out, "0 updated, 0 unchanged") {
		t.Errorf("CityScaffold() first run output = %q, want all files created", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "city.toml")); err != nil {
		t.Errorf("city.toml not materialised: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dir, ".gc")); err != nil || !info.IsDir() {
		t.Errorf(".gc/ not created: %v", err)
	}
}

func TestCityScaffoldJSONIsMachineReadable(t *testing.T) {
	state := testsupport.TempStateHome(t)

	out := captureStdout(t, func() { CityScaffold([]string{"--json"}) })
	var parsed struct {
		Dir   string            `json:"dir"`
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("CityScaffold(--json) output is not JSON: %v\n%s", err, out)
	}
	if parsed.Dir != filepath.Join(state, "gascity", "city") {
		t.Errorf("dir = %q, want under state home %q", parsed.Dir, state)
	}
	if parsed.Files["city.toml"] != "created" {
		t.Errorf("files[city.toml] = %q, want created", parsed.Files["city.toml"])
	}

	// Second run: everything unchanged, still valid JSON.
	out = captureStdout(t, func() { CityScaffold([]string{"--json"}) })
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("second CityScaffold(--json) output is not JSON: %v\n%s", err, out)
	}
	if parsed.Files["city.toml"] != "unchanged" {
		t.Errorf("rerun files[city.toml] = %q, want unchanged", parsed.Files["city.toml"])
	}
}

func TestCityScaffoldHelp(t *testing.T) {
	out := captureStdout(t, func() { CityScaffold([]string{"--help"}) })
	if !strings.Contains(out, "parlay city-scaffold") {
		t.Errorf("CityScaffold(--help) = %q, want the help text", out)
	}
}
