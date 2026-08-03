// Mirrors packages/cli/src/config.test.ts's precedence cases.
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

func TestServerURLFallsBackToDefault(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	if got := ServerURL(); got != DefaultServer {
		t.Errorf("ServerURL() = %q, want %q", got, DefaultServer)
	}
	src := ServerSource()
	if src.Source != SourceDefault || src.URL != DefaultServer {
		t.Errorf("ServerSource() = %+v, want {default %s}", src, DefaultServer)
	}
}

func TestPersistedConfigWinsOverDefault(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	if err := SetPersistedServer("http://macbook:31337"); err != nil {
		t.Fatalf("SetPersistedServer: %v", err)
	}
	if got := ServerURL(); got != "http://macbook:31337" {
		t.Errorf("ServerURL() = %q, want http://macbook:31337", got)
	}
	src := ServerSource()
	if src.Source != SourceConfig || src.URL != "http://macbook:31337" {
		t.Errorf("ServerSource() = %+v", src)
	}
}

func TestEnvWinsOverPersistedConfig(t *testing.T) {
	testsupport.TempStateHome(t)

	if err := SetPersistedServer("http://macbook:31337"); err != nil {
		t.Fatalf("SetPersistedServer: %v", err)
	}
	t.Setenv("PARLAY_SERVER", "http://env-override:9999")

	if got := ServerURL(); got != "http://env-override:9999" {
		t.Errorf("ServerURL() = %q, want http://env-override:9999", got)
	}
	src := ServerSource()
	if src.Source != SourceEnv || src.URL != "http://env-override:9999" {
		t.Errorf("ServerSource() = %+v", src)
	}
}

func TestSetPersistedServerTrimsTrailingSlashesAndClears(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	if err := SetPersistedServer("http://mini1:31337///"); err != nil {
		t.Fatalf("SetPersistedServer: %v", err)
	}
	if got := PersistedServerURL(); got != "http://mini1:31337" {
		t.Errorf("PersistedServerURL() = %q, want http://mini1:31337", got)
	}

	if err := SetPersistedServer(""); err != nil {
		t.Fatalf("SetPersistedServer(clear): %v", err)
	}
	if got := PersistedServerURL(); got != "" {
		t.Errorf("PersistedServerURL() after clear = %q, want empty", got)
	}
	if got := ServerURL(); got != DefaultServer {
		t.Errorf("ServerURL() after clear = %q, want %q", got, DefaultServer)
	}
}

func TestCorruptConfigTreatedAsEmpty(t *testing.T) {
	stateDir := testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "config.json"), []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if got := ServerURL(); got != DefaultServer {
		t.Errorf("ServerURL() with corrupt config = %q, want %q", got, DefaultServer)
	}
}
