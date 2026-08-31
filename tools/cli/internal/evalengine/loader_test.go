package evalengine

import (
	"os"
	"path/filepath"
	"testing"
)

// A minimal valid manifest with a single clear command bound to a distinctive
// phrase, so a test can tell which manifest is live by what the engine fires.
func manifestWithClearPhrase(version, phrase string) string {
	return `{
  "schema": "parlay.commands/v1",
  "version": "` + version + `",
  "commands": [
    { "id": "clear", "phrases": ["` + phrase + `"], "mode": "whole", "priority": 10,
      "emit": { "kind": "sequence", "actions": [ { "verb": "clear" } ] } }
  ]
}`
}

func TestLoadManifestFileValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commands.json")
	if err := os.WriteFile(path, []byte(manifestWithClearPhrase("t1", "wipe it now")), 0o644); err != nil {
		t.Fatal(err)
	}
	man, err := loadManifestFile(path)
	if err != nil {
		t.Fatalf("valid manifest should load: %v", err)
	}
	if len(man.Commands) != 1 || man.Commands[0].ID != "clear" {
		t.Fatalf("unexpected commands: %+v", man.Commands)
	}
}

func TestLoadManifestFileErrors(t *testing.T) {
	dir := t.TempDir()
	t.Run("missing file", func(t *testing.T) {
		if _, err := loadManifestFile(filepath.Join(dir, "nope.json")); err == nil {
			t.Fatal("missing file must error")
		}
	})
	t.Run("malformed json", func(t *testing.T) {
		p := filepath.Join(dir, "bad.json")
		os.WriteFile(p, []byte("{ not json"), 0o644)
		if _, err := loadManifestFile(p); err == nil {
			t.Fatal("malformed json must error")
		}
	})
	t.Run("empty command set rejected", func(t *testing.T) {
		p := filepath.Join(dir, "empty.json")
		os.WriteFile(p, []byte(`{"schema":"parlay.commands/v1","version":"t","commands":[]}`), 0o644)
		if _, err := loadManifestFile(p); err == nil {
			t.Fatal("empty command set must be rejected (never fall open to zero)")
		}
	})
}

// TestHotReloadFailClosed is the core Step-4 guarantee: a valid manifest hot-swaps
// the live set; an invalid one is rejected and the PRIOR GOOD set stays live; a new
// valid one swaps again. Driven deterministically through watcher.check().
func TestHotReloadFailClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commands.json")

	// Manifest A: clear on "wipe it now".
	if err := os.WriteFile(path, []byte(manifestWithClearPhrase("A", "wipe it now")), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	manA, err := loadManifestFile(path)
	if err != nil {
		t.Fatal(err)
	}
	e.SetCommands(manA)

	if got := eval(e, "wipe it now", 1, nil).Fired; got != "clear" {
		t.Fatalf("manifest A should fire clear on its phrase, got %q", got)
	}
	// The embedded default's phrase must NOT be live anymore (A replaced it).
	if got := eval(e, "change inside input", 2, nil).Fired; got != "" {
		t.Fatalf("manifest A replaced the embedded set; old phrase should not fire, got %q", got)
	}

	w := newManifestWatcher(e, path, true)

	// Overwrite with GARBAGE → check() errors, prior good set (A) stays live.
	if err := os.WriteFile(path, []byte("{ this is not valid json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	reloaded, err := w.check()
	if err == nil || reloaded {
		t.Fatalf("garbage manifest must be rejected: reloaded=%v err=%v", reloaded, err)
	}
	if got := eval(e, "wipe it now", 3, nil).Fired; got != "clear" {
		t.Fatalf("fail-closed: prior good set A must stay live after a bad reload, got %q", got)
	}

	// Overwrite with a schema violation (unknown verb) → also rejected, A stays.
	badVerb := `{"schema":"parlay.commands/v1","version":"bad","commands":[{"id":"x","phrases":["boop"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[{"verb":"teleport"}]}}]}`
	os.WriteFile(path, []byte(badVerb), 0o644)
	if reloaded, err := w.check(); err == nil || reloaded {
		t.Fatalf("unknown-verb manifest must be rejected: reloaded=%v err=%v", reloaded, err)
	}
	if got := eval(e, "wipe it now", 4, nil).Fired; got != "clear" {
		t.Fatalf("fail-closed after unknown-verb reload: A must stay live, got %q", got)
	}

	// Overwrite with a VALID manifest B: clear on "erase everything completely".
	os.WriteFile(path, []byte(manifestWithClearPhrase("B", "erase everything completely")), 0o644)
	reloaded, err = w.check()
	if err != nil || !reloaded {
		t.Fatalf("valid manifest B must hot-swap: reloaded=%v err=%v", reloaded, err)
	}
	if got := eval(e, "erase everything completely", 5, nil).Fired; got != "clear" {
		t.Fatalf("manifest B should be live, got %q", got)
	}
	if got := eval(e, "wipe it now", 6, nil).Fired; got != "" {
		t.Fatalf("manifest A's phrase should be gone after B loaded, got %q", got)
	}
}

// TestWatcherUnchangedNoReload proves an unchanged file is not re-applied every tick.
func TestWatcherUnchangedNoReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commands.json")
	os.WriteFile(path, []byte(manifestWithClearPhrase("A", "wipe it now")), 0o644)
	e := NewEngine()
	man, _ := loadManifestFile(path)
	e.SetCommands(man)
	w := newManifestWatcher(e, path, true)
	if reloaded, err := w.check(); err != nil || reloaded {
		t.Fatalf("unchanged file must not reload: reloaded=%v err=%v", reloaded, err)
	}
}

// TestInstallFileSourceEnv proves PARLAY_COMMANDS is honored and applied over the
// embedded default at startup, and the watcher goroutine stops on the stop channel.
func TestInstallFileSourceEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mycommands.json")
	os.WriteFile(path, []byte(manifestWithClearPhrase("env", "obliterate this")), 0o644)
	t.Setenv("PARLAY_COMMANDS", path)

	e := NewEngine()
	stop := make(chan struct{})
	defer close(stop)
	got := installFileSource(e, stop)
	if got != path {
		t.Fatalf("installFileSource returned %q, want %q", got, path)
	}
	if fired := eval(e, "obliterate this", 1, nil).Fired; fired != "clear" {
		t.Fatalf("env manifest should be live, fired %q", fired)
	}
}

// TestInstallFileSourceEnvMissingFileFailsClosed proves a set-but-missing
// PARLAY_COMMANDS leaves the embedded default live (fail-closed), yet still returns
// the path so the watcher can pick the file up if it appears.
func TestInstallFileSourceEnvMissingFileFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-there-yet.json")
	t.Setenv("PARLAY_COMMANDS", path)

	e := NewEngine()
	stop := make(chan struct{})
	defer close(stop)
	got := installFileSource(e, stop)
	if got != path {
		t.Fatalf("installFileSource should still watch a missing env path, got %q", got)
	}
	// Embedded default is still live.
	if fired := eval(e, "change inside input", 1, nil).Fired; fired != "clear" {
		t.Fatalf("embedded default must stay live when env file is missing, fired %q", fired)
	}
}
