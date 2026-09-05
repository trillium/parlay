package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
)

// launchAccountFixture seeds an agent (optionally pinning `account:`), a
// config.toml under the tmp state home freshHome established, and points the
// launch dispatch at a recording stub instead of re-exec'ing this binary
// (which, under `go test`, is the test suite). Returns the path the stub
// records its argv to.
func launchAccountFixture(t *testing.T, identityAccount, configTOML string) string {
	t.Helper()
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{Account: identityAccount})
	if configTOML != "" {
		if err := os.WriteFile(filepath.Join(config.StateHome(), "config.toml"), []byte(configTOML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	record := filepath.Join(bin, "spawn.argv")
	stub := filepath.Join(bin, "fake-spawn")
	script := "#!/bin/sh\n: > " + record + "\nfor a in \"$@\"; do echo \"$a\" >> " + record + "; done\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := launchSpawnCommand
	launchSpawnCommand = func() []string { return []string{stub} }
	t.Cleanup(func() { launchSpawnCommand = orig })
	return record
}

// launchedArgv runs `identity --launch worker` and returns the argv the fake
// spawner recorded, NUL-joined so a flag and its value can be asserted as an
// adjacent pair.
func launchedArgv(t *testing.T, record string) string {
	t.Helper()
	res := args.Parse(string(KindIdentity), []string{"--launch", "worker"}, MemBoolFlags, MemValueFlags)
	captureStdout(t, func() {
		if !HandleLaunch(KindIdentity, res) {
			t.Fatal("HandleLaunch should have handled --launch")
		}
	})
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("spawner was never executed: %v", err)
	}
	return strings.Join(strings.Split(strings.TrimRight(string(got), "\n"), "\n"), "\x00")
}

// `identity --launch` is the DOMINANT respawn route — `parlay reset --reboot`
// and `identity --submit` both funnel through it — so an identity that pins
// an `account:` must come back on that ccjuggler account here too, not just
// via `parlay launch`.
func TestHandleLaunchPassesIdentityAccountToSpawner(t *testing.T) {
	record := launchAccountFixture(t, "acc7", "")

	if argv := launchedArgv(t, record); !strings.Contains(argv, "--account\x00acc7") {
		t.Errorf("spawner argv = %q, want --account acc7 passed through", argv)
	}
}

func TestHandleLaunchFallsBackToConfiguredSpawnAccount(t *testing.T) {
	record := launchAccountFixture(t, "", "spawnAccount = \"acc2\"\n")

	if argv := launchedArgv(t, record); !strings.Contains(argv, "--account\x00acc2") {
		t.Errorf("spawner argv = %q, want the configured spawnAccount passed through", argv)
	}
}

func TestHandleLaunchIdentityAccountBeatsConfiguredDefault(t *testing.T) {
	record := launchAccountFixture(t, "identity-acc", "spawnAccount = \"config-acc\"\n")

	argv := launchedArgv(t, record)
	if !strings.Contains(argv, "--account\x00identity-acc") {
		t.Errorf("spawner argv = %q, want the identity's account to win", argv)
	}
	if strings.Contains(argv, "config-acc") {
		t.Errorf("spawner argv = %q, want the config default not to appear at all", argv)
	}
}

// With nothing configured, --account must be ABSENT rather than empty: the
// spawner exits 2 on a valueless flag, so an empty value would turn "no
// account configured" into a hard launch failure.
func TestHandleLaunchOmitsAccountWhenNoneConfigured(t *testing.T) {
	record := launchAccountFixture(t, "", "")

	if argv := launchedArgv(t, record); strings.Contains(argv, "--account") {
		t.Errorf("spawner argv = %q, want no --account flag at all", argv)
	}
}
