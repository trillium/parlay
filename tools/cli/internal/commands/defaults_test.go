// Mirrors packages/cli/src/commands-defaults.test.ts's cases. State isolated
// to a tmp PARLAY_STATE_HOME per test so this never touches a real
// ~/.parlay/config.toml on the machine running the suite; TestMain in
// this package already clears PARLAY_SPAWN_DEFAULT_ACCOUNT.
package commands

import (
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

func TestDefaultsBareShowsEmptyValues(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	out := captureStdout(t, func() { Defaults(nil) })
	if !strings.Contains(out, "server:       http://localhost:4242 (source: default)") {
		t.Errorf("Defaults(nil) output = %q, want default server URL + source", out)
	}
	if !strings.Contains(out, "spawnAccount: (none") {
		t.Errorf("Defaults(nil) output = %q, want the unset spawnAccount hint", out)
	}
}

func TestDefaultsBareShowsPersistedValues(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")
	if err := config.SetSpawnAccount("acc2"); err != nil {
		t.Fatalf("SetSpawnAccount: %v", err)
	}

	out := captureStdout(t, func() { Defaults(nil) })
	if !strings.Contains(out, "spawnAccount: acc2 (source: config") {
		t.Errorf("Defaults(nil) output = %q, want the persisted account + config source", out)
	}
	if !strings.Contains(out, config.SpawnAccountConfigPath()) {
		t.Errorf("Defaults(nil) output = %q, want the config.toml path shown", out)
	}
}

func TestDefaultsBareShowsEnvOverride(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")
	if err := config.SetSpawnAccount("acc2"); err != nil {
		t.Fatalf("SetSpawnAccount: %v", err)
	}
	t.Setenv(config.SpawnAccountEnv, "env-acc")

	out := captureStdout(t, func() { Defaults(nil) })
	if !strings.Contains(out, "spawnAccount: env-acc (source: env") {
		t.Errorf("Defaults(nil) output = %q, want the env override + env source", out)
	}
}

func TestDefaultsSetPersistsAndReflects(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	out := captureStdout(t, func() { Defaults([]string{"set", "account", "acc2"}) })
	if !strings.Contains(out, "persisted default spawn account: acc2") {
		t.Errorf("Defaults(set) output = %q, want a persisted-account confirmation", out)
	}
	if got := config.SpawnAccount(); got != "acc2" {
		t.Errorf("config.SpawnAccount() after defaults set = %q, want acc2", got)
	}

	out = captureStdout(t, func() { Defaults(nil) })
	if !strings.Contains(out, "spawnAccount: acc2 (source: config") {
		t.Errorf("Defaults(nil) after set = %q, want the persisted account shown", out)
	}
}

func TestDefaultsClearRemovesAccount(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")
	if err := config.SetSpawnAccount("acc2"); err != nil {
		t.Fatalf("SetSpawnAccount: %v", err)
	}

	out := captureStdout(t, func() { Defaults([]string{"clear", "account"}) })
	if !strings.Contains(out, "cleared persisted spawn account") {
		t.Errorf("Defaults(clear) output = %q, want a cleared confirmation", out)
	}

	out = captureStdout(t, func() { Defaults(nil) })
	if !strings.Contains(out, "spawnAccount: (none") {
		t.Errorf("Defaults(nil) after clear = %q, want the unset hint back", out)
	}
}

func TestDefaultsSetMissingArgIsUsageError(t *testing.T) {
	testsupport.TempStateHome(t)
	cases := [][]string{
		{"set"},
		{"set", "account"},
		{"set", "bogus", "acc2"}, // the middle positional must be "account"
	}
	for _, argv := range cases {
		code, exited := withExitTrap(t, func() { Defaults(argv) })
		if !exited || code != config.ExitUsage {
			t.Errorf("Defaults(%v) exit = (%d, %v), want (%d, true)", argv, code, exited, config.ExitUsage)
		}
	}
}

func TestDefaultsUnknownSubcommandIsUsageError(t *testing.T) {
	testsupport.TempStateHome(t)

	code, exited := withExitTrap(t, func() { Defaults([]string{"bogus"}) })
	if !exited || code != config.ExitUsage {
		t.Errorf("Defaults(bogus) exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
}
