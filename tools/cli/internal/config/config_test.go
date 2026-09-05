// Mirrors packages/cli/src/config.test.ts's precedence cases.
package config

import (
	"os"
	"path/filepath"
	"strings"
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

	if err := SetPersistedServer("http://macbook:4242"); err != nil {
		t.Fatalf("SetPersistedServer: %v", err)
	}
	if got := ServerURL(); got != "http://macbook:4242" {
		t.Errorf("ServerURL() = %q, want http://macbook:4242", got)
	}
	src := ServerSource()
	if src.Source != SourceConfig || src.URL != "http://macbook:4242" {
		t.Errorf("ServerSource() = %+v", src)
	}
}

func TestEnvWinsOverPersistedConfig(t *testing.T) {
	testsupport.TempStateHome(t)

	if err := SetPersistedServer("http://macbook:4242"); err != nil {
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

	if err := SetPersistedServer("http://mini1:4242///"); err != nil {
		t.Fatalf("SetPersistedServer: %v", err)
	}
	if got := PersistedServerURL(); got != "http://mini1:4242" {
		t.Errorf("PersistedServerURL() = %q, want http://mini1:4242", got)
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

// The env var is the spawn pipeline's highest-precedence source, so the Go
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

// The retired bash spawner tested the env var with `[ -z ]`, so an env var that is set
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

func TestSetSpawnAccountWritesLineAndRoundTrips(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	t.Setenv(SpawnAccountEnv, "")

	if err := SetSpawnAccount("acc2"); err != nil {
		t.Fatalf("SetSpawnAccount: %v", err)
	}
	if got := SpawnAccount(); got != "acc2" {
		t.Errorf("SpawnAccount() after set = %q, want %q", got, "acc2")
	}
	body, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config.toml): %v", err)
	}
	if string(body) != "spawnAccount = \"acc2\"" {
		t.Errorf("config.toml = %q, want %q", body, "spawnAccount = \"acc2\"")
	}
}

func TestSetSpawnAccountPreservesSurroundingFile(t *testing.T) {
	// robots-ni5p: the TOML writer must not clobber the [spawn] table or other
	// top-level keys — rewriting the file from scratch would. Only the
	// spawnAccount line may change.
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	writeTOML(t, home, "# a comment\n\n[spawn]\nbeads_required = true\n")
	t.Setenv(SpawnAccountEnv, "")

	if err := SetSpawnAccount("acc2"); err != nil {
		t.Fatalf("SetSpawnAccount: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config.toml): %v", err)
	}
	want := "# a comment\n\nspawnAccount = \"acc2\"\n[spawn]\nbeads_required = true\n"
	if string(body) != want {
		t.Errorf("config.toml = %q, want %q", body, want)
	}
	if got := SpawnAccount(); got != "acc2" {
		t.Errorf("SpawnAccount() = %q, want acc2", got)
	}
}

func TestSetSpawnAccountReplacesInPlace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	writeTOML(t, home, "# note\nspawnAccount = \"old\"\n[spawn]\nbeads_required = true\n")
	t.Setenv(SpawnAccountEnv, "")

	if err := SetSpawnAccount("acc2"); err != nil {
		t.Fatalf("SetSpawnAccount: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config.toml): %v", err)
	}
	want := "# note\nspawnAccount = \"acc2\"\n[spawn]\nbeads_required = true\n"
	if string(body) != want {
		t.Errorf("config.toml = %q, want %q", body, want)
	}
	if got := SpawnAccount(); got != "acc2" {
		t.Errorf("SpawnAccount() = %q, want acc2", got)
	}
}

func TestSetSpawnAccountInsertsBeforeTableNotAfter(t *testing.T) {
	// A spawnAccount nested under [spawn] is a different key the reader never
	// sees, so an insert must land BEFORE the first table header.
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	writeTOML(t, home, "[spawn]\nbeads_required = true\n")
	t.Setenv(SpawnAccountEnv, "")

	if err := SetSpawnAccount("acc2"); err != nil {
		t.Fatalf("SetSpawnAccount: %v", err)
	}
	if got := SpawnAccount(); got != "acc2" {
		t.Errorf("SpawnAccount() = %q, want acc2 — the inserted key must stay in the top-level table", got)
	}
}

func TestSetSpawnAccountClearRemovesLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	writeTOML(t, home, "# note\nspawnAccount = \"acc2\"\n[spawn]\nbeads_required = true\n")
	t.Setenv(SpawnAccountEnv, "")

	if err := SetSpawnAccount(""); err != nil {
		t.Fatalf("SetSpawnAccount(clear): %v", err)
	}
	if got := SpawnAccount(); got != "" {
		t.Errorf("SpawnAccount() after clear = %q, want empty", got)
	}
	body, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config.toml): %v", err)
	}
	want := "# note\n[spawn]\nbeads_required = true\n"
	if string(body) != want {
		t.Errorf("config.toml = %q, want %q", body, want)
	}
}

func TestSetSpawnAccountClearOfClearFileIsNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	t.Setenv(SpawnAccountEnv, "")
	writeTOML(t, home, "# note\n[spawn]\nbeads_required = true\n")

	if err := SetSpawnAccount(""); err != nil {
		t.Fatalf("SetSpawnAccount(clear): %v", err)
	}
	body, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config.toml): %v", err)
	}
	if string(body) != "# note\n[spawn]\nbeads_required = true\n" {
		t.Errorf("config.toml rewritten by a no-op clear = %q", body)
	}
}

func TestSetSpawnAccountCreatesMissingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	t.Setenv(SpawnAccountEnv, "")

	if err := SetSpawnAccount("acc2"); err != nil {
		t.Fatalf("SetSpawnAccount: %v", err)
	}
	if got := SpawnAccount(); got != "acc2" {
		t.Errorf("SpawnAccount() = %q, want acc2", got)
	}
}

func TestSetSpawnAccountNestedKeyIsPassedThroughAndTopLevelInserted(t *testing.T) {
	// When config.toml already has a spawnAccount nested under [spawn] (a
	// hand-edited file, never produced by SetSpawnAccount), SetSpawnAccount must
	// NOT replace that nested line and must insert a top-level key before [spawn]
	// so SpawnAccount() resolves correctly. The nested line must be untouched.
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	t.Setenv(SpawnAccountEnv, "")
	initial := "[spawn]\nspawnAccount = \"nested-acc\"\nbeads_required = true\n"
	writeTOML(t, home, initial)

	if err := SetSpawnAccount("acc2"); err != nil {
		t.Fatalf("SetSpawnAccount: %v", err)
	}
	if got := SpawnAccount(); got != "acc2" {
		t.Errorf("SpawnAccount() = %q, want acc2 — top-level key must be inserted, not nested replacement", got)
	}
	body, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config.toml): %v", err)
	}
	want := "spawnAccount = \"acc2\"\n[spawn]\nspawnAccount = \"nested-acc\"\nbeads_required = true\n"
	if string(body) != want {
		t.Errorf("config.toml = %q, want %q — nested line must be untouched", body, want)
	}
}

func TestSetSpawnAccountReplacesWhenEnvOverrides(t *testing.T) {
	// The write must not be defeated by a live env override: set persists to
	// the file regardless, and SpawnAccount still reports the env value — the
	// env var is the spawn pipeline's highest-precedence source.
	home := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", home)
	t.Setenv(SpawnAccountEnv, "env-acc")

	if err := SetSpawnAccount("acc2"); err != nil {
		t.Fatalf("SetSpawnAccount: %v", err)
	}
	if got := SpawnAccount(); got != "env-acc" {
		t.Errorf("SpawnAccount() = %q, want env-acc (env wins over the file)", got)
	}
	body, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config.toml): %v", err)
	}
	if !strings.Contains(string(body), `spawnAccount = "acc2"`) {
		t.Errorf("config.toml = %q, want it to persist acc2 under a live env override", body)
	}
}
