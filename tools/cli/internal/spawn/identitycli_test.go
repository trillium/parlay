package spawn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeParlayOnPATH installs a `parlay` shim that records its argv NUL-joined
// into record, so tests can assert what registerIdentity shells out to.
func fakeParlayOnPATH(t *testing.T, record string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n: > " + record + "\nfor a in \"$@\"; do echo \"$a\" >> " + record + "; done\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "parlay"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func recordedArgv(t *testing.T, record string) string {
	t.Helper()
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("parlay shim was never executed: %v", err)
	}
	return strings.Join(strings.Split(strings.TrimRight(string(got), "\n"), "\n"), "\x00")
}

// The spawn-time-account writer (task-0d6mi): registerIdentity must forward
// the resolved --account into the `parlay identity --register` subprocess so
// the agent's identity.md records which ccjuggler account it relaunches
// under. Without this the pin only ever fired when a human hand-edited the
// frontmatter.
func TestRegisterIdentityForwardsAccount(t *testing.T) {
	record := filepath.Join(t.TempDir(), "parlay.argv")
	fakeParlayOnPATH(t, record)

	registerIdentity(registerIdentityOptions{
		AgentID: "acctest",
		Name:    "Acc Test",
		Color:   "#010203",
		Cwd:     "/tmp/acctest",
		Mode:    "report",
		Account: "acc7",
	})

	argv := recordedArgv(t, record)
	if !strings.Contains(argv, "--register") {
		t.Errorf("argv = %q, want a --register call", argv)
	}
	if !strings.Contains(argv, "--account\x00acc7") {
		t.Errorf("argv = %q, want --account acc7 forwarded into registration", argv)
	}
}

// With no account set, registerIdentity must not emit a --account flag at
// all — the downstream identity --register only writes non-empty fields, and
// a valueless flag would corrupt the whole frontmatter write.
func TestRegisterIdentityOmitsAccountWhenEmpty(t *testing.T) {
	record := filepath.Join(t.TempDir(), "parlay.argv")
	fakeParlayOnPATH(t, record)

	registerIdentity(registerIdentityOptions{
		AgentID: "acctest",
		Name:    "Acc Test",
		Color:   "#010203",
		Cwd:     "/tmp/acctest",
		Mode:    "report",
	})

	argv := recordedArgv(t, record)
	if strings.Contains(argv, "--account") {
		t.Errorf("argv = %q, want no --account flag when none is set", argv)
	}
}

// An explicit `--account acc7` on the spawn argv must survive parsing and be
// persisted into the agent's identity.md registration (task-0d6mi).
func TestExplicitAccountFlagIsPersistedIntoRegistration(t *testing.T) {
	t.Setenv("PARLAY_STATE_HOME", t.TempDir())
	t.Setenv("PARLAY_SPAWN_DEFAULT_ACCOUNT", "ambient-default")

	opts := defaultSpawnOptions()
	if err := parseTailFlags([]string{"--model", "sonnet", "--account", "acc7"}, &opts, false, true); err != nil {
		t.Fatalf("parseTailFlags: %v", err)
	}
	if opts.Account != "acc7" {
		t.Fatalf("opts.Account = %q, want acc7", opts.Account)
	}

	record := filepath.Join(t.TempDir(), "parlay.argv")
	fakeParlayOnPATH(t, record)
	registerIdentity(registerIdentityOptions{
		AgentID: "acctest", Name: "Acc Test", Color: "#010203",
		Cwd: "/tmp/acctest", Mode: "report", Account: identityAccount(opts),
	})

	if argv := recordedArgv(t, record); !strings.Contains(argv, "--account\x00acc7") {
		t.Errorf("argv = %q, want --account acc7 persisted into registration", argv)
	}
}

// A spawn with NO --account must leave identity.md account-free even when an
// ambient default (PARLAY_SPAWN_DEFAULT_ACCOUNT / config.toml spawnAccount)
// resolved one: the default still drives this launch's token resolution, but
// pinning it into the frontmatter would outrank config forever and make a
// later `parlay defaults set account` rotation invisible to the agent.
func TestNoAccountFlagLeavesIdentityAccountUnset(t *testing.T) {
	t.Setenv("PARLAY_STATE_HOME", t.TempDir())
	t.Setenv("PARLAY_SPAWN_DEFAULT_ACCOUNT", "ambient-default")

	opts := defaultSpawnOptions()
	if err := parseTailFlags([]string{"--model", "sonnet"}, &opts, false, true); err != nil {
		t.Fatalf("parseTailFlags: %v", err)
	}
	if opts.Account != "ambient-default" {
		t.Fatalf("opts.Account = %q, want the ambient default to still drive this launch", opts.Account)
	}
	if opts.AccountFromFlag {
		t.Fatal("AccountFromFlag = true with no --account on the argv")
	}

	record := filepath.Join(t.TempDir(), "parlay.argv")
	fakeParlayOnPATH(t, record)
	registerIdentity(registerIdentityOptions{
		AgentID: "acctest", Name: "Acc Test", Color: "#010203",
		Cwd: "/tmp/acctest", Mode: "report", Account: identityAccount(opts),
	})

	if argv := recordedArgv(t, record); strings.Contains(argv, "--account") {
		t.Errorf("argv = %q, want no --account — the ambient default must not be pinned into identity.md", argv)
	}
}
