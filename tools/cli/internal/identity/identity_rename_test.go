// Integration tests for `parlay identity --rename <old> --to <new>`: store
// move, context.json/frontmatter id rewrite, override application,
// reincarnations log, server re-registration, the clobber guard, and
// --preserve.
//
// Mirrors packages/cli/src/commands-identity-rename.test.ts.
package identity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameMovesStoreAndReregisters(t *testing.T) {
	h := startHarness(t)
	home := freshHome(t)
	seedAgent(t, home, "old-id", seedOpts{Name: "Old", Color: "#ff0000", Reincarnation: true})

	captureStdout(t, func() {
		CmdIdentity([]string{"--rename", "old-id", "--to", "new-id"})
	})

	if _, err := os.Stat(filepath.Join(home, "old-id")); err == nil {
		t.Error("old-id dir should no longer exist")
	}
	if _, err := os.Stat(filepath.Join(home, "new-id")); err != nil {
		t.Error("new-id dir should exist")
	}

	ctxData, err := os.ReadFile(filepath.Join(home, "new-id", "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ctx map[string]string
	_ = json.Unmarshal(ctxData, &ctx)
	if ctx["id"] != "new-id" {
		t.Errorf("ctx.id = %q, want new-id", ctx["id"])
	}
	if ctx["name"] != "Old" {
		t.Errorf("ctx.name = %q, want Old (preserved)", ctx["name"])
	}

	fm := ReadFrontmatter(filepath.Join(home, "new-id", "identity.md"))
	if fm.Get("id") != "new-id" {
		t.Errorf("fm.id = %q, want new-id", fm.Get("id"))
	}

	logData, err := os.ReadFile(filepath.Join(home, "new-id", "reincarnations.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(logData), "\n"), "\n")
	var entry map[string]string
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["event"] != "renamed" || entry["from"] != "old-id" || entry["to"] != "new-id" {
		t.Errorf("reincarnation entry = %+v", entry)
	}
	if !strings.Contains(string(logData), `"agent":"old-id"`) {
		t.Error("expected original reincarnations.log content preserved")
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.registerBodies) != 1 {
		t.Fatalf("expected 1 register-agent call, got %d", len(h.registerBodies))
	}
	body := h.registerBodies[0]
	if body["id"] != "new-id" || body["name"] != "Old" || body["color"] != "#ff0000" {
		t.Errorf("register body = %+v", body)
	}
}

func TestRenameAppliesOverrides(t *testing.T) {
	startHarness(t)
	home := freshHome(t)
	seedAgent(t, home, "old-id", seedOpts{Name: "Old", Color: "#ff0000"})

	captureStdout(t, func() {
		CmdIdentity([]string{"--rename", "old-id", "--to", "new-id", "--name", "Renamed", "--color", "#00ff00"})
	})

	ctxData, _ := os.ReadFile(filepath.Join(home, "new-id", "context.json"))
	var ctx map[string]string
	_ = json.Unmarshal(ctxData, &ctx)
	if ctx["id"] != "new-id" || ctx["name"] != "Renamed" || ctx["color"] != "#00ff00" {
		t.Errorf("context.json = %+v", ctx)
	}

	fm := ReadFrontmatter(filepath.Join(home, "new-id", "identity.md"))
	if fm.Get("name") != "Renamed" {
		t.Errorf("fm.name = %q, want Renamed", fm.Get("name"))
	}
	if fm.Get("color") != "#00ff00" {
		t.Errorf("fm.color = %q, want #00ff00", fm.Get("color"))
	}
}

func TestRenameRefusesToClobberExistingTarget(t *testing.T) {
	h := startHarness(t)
	home := freshHome(t)
	seedAgent(t, home, "old-id", seedOpts{})
	seedAgent(t, home, "taken", seedOpts{})

	_, code, exited := runCapturingExit(t, func() {
		CmdIdentity([]string{"--rename", "old-id", "--to", "taken"})
	})
	if !exited {
		t.Fatal("expected exit")
	}
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if _, err := os.Stat(filepath.Join(home, "old-id")); err != nil {
		t.Error("old-id should still exist")
	}
	if _, err := os.Stat(filepath.Join(home, "taken")); err != nil {
		t.Error("taken should still exist")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.registerBodies) != 0 {
		t.Errorf("expected no register-agent calls, got %d", len(h.registerBodies))
	}
}

func TestRenameErrorsWhenToMissing(t *testing.T) {
	home := freshHome(t)
	seedAgent(t, home, "old-id", seedOpts{})

	_, code, exited := runCapturingExit(t, func() {
		CmdIdentity([]string{"--rename", "old-id"})
	})
	if !exited || code != 2 {
		t.Fatalf("exited=%v code=%d, want exited with 2", exited, code)
	}
	if _, err := os.Stat(filepath.Join(home, "old-id")); err != nil {
		t.Error("old-id should still exist")
	}
}

func TestRenamePreserveClearsEphemeralMarker(t *testing.T) {
	startHarness(t)
	home := freshHome(t)
	seedAgent(t, home, "eph-deadbeef", seedOpts{Ephemeral: true})

	captureStdout(t, func() {
		CmdIdentity([]string{"--rename", "eph-deadbeef", "--to", "durable", "--preserve"})
	})

	fm := ReadFrontmatter(filepath.Join(home, "durable", "identity.md"))
	if fm.Has("ephemeral") {
		t.Errorf("expected ephemeral cleared, got %q", fm.Get("ephemeral"))
	}
	if fm.Get("id") != "durable" {
		t.Errorf("fm.id = %q, want durable", fm.Get("id"))
	}
}

func TestRenameWithoutPreserveKeepsEphemeralMarker(t *testing.T) {
	startHarness(t)
	home := freshHome(t)
	seedAgent(t, home, "eph-cafef00d", seedOpts{Ephemeral: true})

	captureStdout(t, func() {
		CmdIdentity([]string{"--rename", "eph-cafef00d", "--to", "eph-newname1"})
	})

	fm := ReadFrontmatter(filepath.Join(home, "eph-newname1", "identity.md"))
	if fm.Get("ephemeral") != "true" {
		t.Errorf("fm.ephemeral = %q, want true", fm.Get("ephemeral"))
	}
}
