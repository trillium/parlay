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
	return installRecordingSpawner(t)
}

// installRecordingSpawner puts a parlay-spawn on PATH that records its argv
// one arg per line, and returns the path it records to.
func installRecordingSpawner(t *testing.T) string {
	t.Helper()
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
	return relaunchedArgv(t, "worker", record)
}

// relaunchedArgv runs `identity --launch <id>` for the given agent id and
// returns the argv the fake spawner recorded, NUL-joined.
func relaunchedArgv(t *testing.T, id, record string) string {
	t.Helper()
	res := args.Parse(string(KindIdentity), []string{"--launch", id}, MemBoolFlags, MemValueFlags)
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

// An UNPINNED identity must relaunch with no --account at all, even when a
// config-level default exists. The spawn pipeline resolves that default
// itself, so the agent still lands on it — but synthesizing it into the argv
// would make the pipeline read it as an explicit --account and persist it
// into identity.md (task-0d6mi's writer), pinning today's default forever and
// making every later `parlay defaults set account` rotation invisible to this
// agent.
func TestHandleLaunchOmitsConfiguredDefaultForUnpinnedIdentity(t *testing.T) {
	record := launchAccountFixture(t, "", "spawnAccount = \"acc2\"\n")

	argv := launchedArgv(t, record)
	if strings.Contains(argv, "--account") {
		t.Errorf("spawner argv = %q, want no --account — the config default must stay live, not be pinned", argv)
	}
	if strings.Contains(argv, "acc2") {
		t.Errorf("spawner argv = %q, want the config default absent from the relaunch argv", argv)
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

// End-to-end writer loop (task-0d6mi): `identity --register --account acc7`
// must record the account in identity.md with NO hand-seeded frontmatter,
// and a subsequent relaunch must come back on acc7. This closes the gap the
// fixture-based tests above leave open — they seed `account:` by hand, which
// is exactly why the missing register writer never surfaced as a failure.
func TestRegisterWritesAccountAndRelaunchUsesIt(t *testing.T) {
	startHarness(t)
	home := freshHome(t)

	// Spawn path: register a fresh agent (no prior identity.md) under acc7.
	captureStdout(t, func() {
		CmdIdentity([]string{
			"--register", "--agent", "acctest", "--name", "Acc Test", "--color", "#010203",
			"--cwd", "/tmp/acctest", "--account", "acc7",
		})
	})

	// The account must have landed in identity.md frontmatter.
	fm := ReadFrontmatter(filepath.Join(home, "acctest", "identity.md"))
	if got := fm.Get("account"); got != "acc7" {
		t.Fatalf("identity.md account = %q, want acc7 (register must write it)", got)
	}

	// Relaunch path: `identity --launch` must forward acc7 to the spawner.
	argv := relaunchedArgv(t, "acctest", installRecordingSpawner(t))
	if !strings.Contains(argv, "--account\x00acc7") {
		t.Errorf("relaunch spawner argv = %q, want --account acc7 (come back on acc7)", argv)
	}
}
