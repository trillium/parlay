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

func TestSpawnAccountReadsTopLevelKeyFromConfigTOML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	t.Setenv(SpawnAccountEnv, "")
	writeTOML(t, home, "spawnAccount = \"acc2\"\n\n[spawn]\nbeads_required = true\n")

	if got := SpawnAccount(); got != "acc2" {
		t.Errorf("SpawnAccount() = %q, want %q", got, "acc2")
	}
}

// The env var is bin/parlay-spawn's highest-precedence source, so the Go
// resolution must not out-rank it with the config file.
func TestSpawnAccountEnvBeatsConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	t.Setenv(SpawnAccountEnv, "env-acc")
	writeTOML(t, home, "spawnAccount = \"file-acc\"\n")

	if got := SpawnAccount(); got != "env-acc" {
		t.Errorf("SpawnAccount() = %q, want the env override", got)
	}
}

// bin/parlay-spawn tests the env var with `[ -z ]`, so an env var that is set
// but empty falls THROUGH to the config file rather than disabling it. Go
// must agree, or the same box resolves two different accounts depending on
// which spawner is installed.
func TestSpawnAccountEmptyEnvFallsThroughToConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	t.Setenv(SpawnAccountEnv, "")
	writeTOML(t, home, "spawnAccount = \"file-acc\"\n")

	if got := SpawnAccount(); got != "file-acc" {
		t.Errorf("SpawnAccount() = %q, want the config fallback", got)
	}
}

// A whitespace-only env var is where Go and bash genuinely differ: `[ -z " " ]`
// is false, so bash would take " " as the account. Go treats it as unset. No
// parity claim here — an account name is a keychain-service suffix, so a
// whitespace-only one resolves no token under either spawner, and falling
// through is the recoverable side of that disagreement.
func TestSpawnAccountWhitespaceEnvFallsThroughToConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	t.Setenv(SpawnAccountEnv, "   ")
	writeTOML(t, home, "spawnAccount = \"file-acc\"\n")

	if got := SpawnAccount(); got != "file-acc" {
		t.Errorf("SpawnAccount() = %q, want the config fallback", got)
	}
}

func TestSpawnAccountEmptyWhenUnconfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	t.Setenv(SpawnAccountEnv, "")

	if got := SpawnAccount(); got != "" {
		t.Errorf("SpawnAccount() with no config = %q, want empty", got)
	}
	writeTOML(t, home, "[spawn]\nbeads_required = true\n")
	if got := SpawnAccount(); got != "" {
		t.Errorf("SpawnAccount() with no spawnAccount key = %q, want empty", got)
	}
}

// A `spawnAccount` nested inside a table is a DIFFERENT key than the
// top-level one python3's tomllib.get("spawnAccount") returns. Reading it
// would spawn agents under an account the bash spawner never picks.
func TestSpawnAccountIgnoresKeyNestedInATable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	t.Setenv(SpawnAccountEnv, "")
	writeTOML(t, home, "[spawn]\nspawnAccount = \"nested-acc\"\n")

	if got := SpawnAccount(); got != "" {
		t.Errorf("SpawnAccount() = %q, want empty — the key is not top-level", got)
	}
}

func TestSpawnAccountValueForms(t *testing.T) {
	cases := []struct{ line, want string }{
		{`spawnAccount = "acc2"`, "acc2"},
		{`spawnAccount='acc2'`, "acc2"},
		{`spawnAccount   =   "acc2"   `, "acc2"},
		{`spawnAccount = "acc2" # the work account`, "acc2"},
		{`spawnAccount = acc2 # unquoted`, "acc2"},
		{`spawnAccount = "acc#2"`, "acc#2"},
		{`spawnAccount = ""`, ""},
		// Unterminated quotes must not yield a half-parsed guess: an account
		// the spawner cannot resolve exits non-zero, which is worse than no
		// account at all.
		{`spawnAccount = "acc`, ""},
		{`spawnAccount = 'acc`, ""},
	}
	for _, c := range cases {
		home := t.TempDir()
		t.Setenv("PARLAY_STATE_HOME", home)
		t.Setenv(SpawnAccountEnv, "")
		writeTOML(t, home, c.line+"\n")
		if got := SpawnAccount(); got != c.want {
			t.Errorf("SpawnAccount() for %q = %q, want %q", c.line, got, c.want)
		}
	}
}

func writeTOML(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
