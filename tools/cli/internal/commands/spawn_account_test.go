// Mirrors commands-spawn-account.ts's contract (TS tree @ 871b3f8f^; the TS
// file shipped without its own test file, so these cases encode the ported
// behavior directly). State isolated to a tmp PARLAY_STATE_HOME per test;
// TestMain in this package already clears PARLAY_SPAWN_DEFAULT_ACCOUNT.
package commands

import (
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

func TestSpawnAccountBareShowsNoneHint(t *testing.T) {
	testsupport.TempStateHome(t)

	out := captureStdout(t, func() { SpawnAccount(nil) })
	if !strings.Contains(out, "(none — set with: parlay spawn-account set <account>)") {
		t.Errorf("SpawnAccount(nil) output = %q, want the unset hint", out)
	}
}

func TestSpawnAccountSetPersistsAndBareShowsIt(t *testing.T) {
	testsupport.TempStateHome(t)

	out := captureStdout(t, func() { SpawnAccount([]string{"set", "acc2"}) })
	if !strings.Contains(out, "persisted default spawn account: acc2") {
		t.Errorf("SpawnAccount(set acc2) output = %q, want a persisted-account confirmation", out)
	}
	if !strings.Contains(out, config.SpawnAccountConfigPath()) {
		t.Errorf("SpawnAccount(set acc2) output = %q, want the config.toml path shown", out)
	}
	if got := config.PersistedSpawnAccount(); got != "acc2" {
		t.Errorf("PersistedSpawnAccount() after set = %q, want acc2", got)
	}

	out = captureStdout(t, func() { SpawnAccount(nil) })
	if !strings.Contains(out, "acc2 (source: config, ") {
		t.Errorf("SpawnAccount(nil) after set = %q, want the persisted account + config source", out)
	}
}

// The show form reads the config file only — TS's persistedSpawnAccount()
// never consulted PARLAY_SPAWN_DEFAULT_ACCOUNT, unlike the spawn-time
// resolver (and unlike the Go-only `parlay defaults` show).
func TestSpawnAccountBareIgnoresEnvOverride(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv(config.SpawnAccountEnv, "env-acc")

	out := captureStdout(t, func() { SpawnAccount(nil) })
	if strings.Contains(out, "env-acc") {
		t.Errorf("SpawnAccount(nil) output = %q, must not show the env override", out)
	}
	if !strings.Contains(out, "(none") {
		t.Errorf("SpawnAccount(nil) output = %q, want the unset hint despite env override", out)
	}
}

func TestSpawnAccountClearRemovesAccount(t *testing.T) {
	testsupport.TempStateHome(t)
	if err := config.SetSpawnAccount("acc2"); err != nil {
		t.Fatalf("SetSpawnAccount: %v", err)
	}

	out := captureStdout(t, func() { SpawnAccount([]string{"clear"}) })
	if !strings.Contains(out, "cleared persisted default spawn account") {
		t.Errorf("SpawnAccount(clear) output = %q, want a cleared confirmation", out)
	}
	if got := config.PersistedSpawnAccount(); got != "" {
		t.Errorf("PersistedSpawnAccount() after clear = %q, want empty", got)
	}
}

// Clearing must not clobber the rest of config.toml — SetSpawnAccount's
// whole reason for existing is preserving the [spawn] table (robots-ni5p).
func TestSpawnAccountClearPreservesOtherKeys(t *testing.T) {
	testsupport.TempStateHome(t)
	captureStdout(t, func() { SpawnAccount([]string{"set", "acc2"}) })
	if err := config.SetPersistedServer("http://mini1:4242"); err != nil {
		t.Fatalf("SetPersistedServer: %v", err)
	}

	captureStdout(t, func() { SpawnAccount([]string{"clear"}) })
	if got := config.PersistedServerURL(); got != "http://mini1:4242" {
		t.Errorf("PersistedServerURL() after spawn-account clear = %q, want it untouched", got)
	}
}

func TestSpawnAccountSetWithoutAccountDiesUsage(t *testing.T) {
	testsupport.TempStateHome(t)

	code, exited := withExitTrap(t, func() { SpawnAccount([]string{"set"}) })
	if !exited || code != config.ExitUsage {
		t.Errorf("SpawnAccount(set) with no account exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
}

func TestSpawnAccountUnknownSubcommandDiesUsage(t *testing.T) {
	testsupport.TempStateHome(t)

	code, exited := withExitTrap(t, func() { SpawnAccount([]string{"bogus"}) })
	if !exited || code != config.ExitUsage {
		t.Errorf("SpawnAccount(bogus) exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
}

// --help prints the full USAGE fallback (spawn-account has no per-command
// HELP entry — TS help.ts had none either) and does no work.
func TestSpawnAccountHelpFallsBackToUsageAndDoesNothing(t *testing.T) {
	testsupport.TempStateHome(t)

	out := captureStdout(t, func() { SpawnAccount([]string{"--help", "set", "acc2"}) })
	if !strings.Contains(out, "parlay — talk to a Parlay chat server") {
		t.Errorf("SpawnAccount(--help) output = %q, want the full USAGE fallback", out)
	}
	if got := config.PersistedSpawnAccount(); got != "" {
		t.Errorf("PersistedSpawnAccount() after --help = %q, want no write", got)
	}
}
