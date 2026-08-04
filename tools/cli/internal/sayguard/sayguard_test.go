// Covers the identity.md pointer reader and session-start sentinel used by
// the create->submit chat guard. Mirrors packages/cli/src/say-guard.test.ts.
// The warn condition itself is exercised end-to-end through
// DetectUnsubmittedHandoff in resolvehandoff_test.go.
package sayguard

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// freshAgentHome points PARLAY_AGENT_HOME at a fresh t.TempDir() and returns it.
func freshAgentHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PARLAY_AGENT_HOME", home)
	return home
}

// seedIdentity writes an identity.md for agent under a fresh PARLAY_AGENT_HOME.
func seedIdentity(t *testing.T, agent, body string) string {
	t.Helper()
	home := freshAgentHome(t)
	dir := filepath.Join(home, agent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

// ── PinnedHandoffID ─────────────────────────────────────────────────────

func TestPinnedHandoffIDReadsPointerFromIdentityMd(t *testing.T) {
	seedIdentity(t, "mayor",
		"---\nid: mayor\n---\n# Identity — mayor\n\n> 📎 Handoff: handoff-1bk — run `handoff show handoff-1bk` for full session state\n")
	if got := PinnedHandoffID("mayor"); got != "handoff-1bk" {
		t.Errorf("got %q, want handoff-1bk", got)
	}
}

func TestPinnedHandoffIDEmptyWhenNoPointer(t *testing.T) {
	seedIdentity(t, "mayor", "---\nid: mayor\n---\n# Identity — mayor\n\n- [2026-07-20] some fact\n")
	if got := PinnedHandoffID("mayor"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestPinnedHandoffIDEmptyWhenIdentityMdAbsent(t *testing.T) {
	freshAgentHome(t) // no <agent>/identity.md
	if got := PinnedHandoffID("mayor"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ── ReadSessionStartMs / WriteSessionStartOnce ─────────────────────────

func TestReadSessionStartMsEmptyWhenAbsent(t *testing.T) {
	freshAgentHome(t)
	if _, ok := ReadSessionStartMs("brain-dev"); ok {
		t.Error("expected ok=false when session-start file is absent")
	}
}

func TestReadSessionStartMsParsesEpochSeconds(t *testing.T) {
	home := freshAgentHome(t)
	epochSec := time.Now().Unix() - 100
	dir := filepath.Join(home, "brain-dev")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session-start"), []byte(strconv.FormatInt(epochSec, 10)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadSessionStartMs("brain-dev")
	if !ok || got != epochSec*1000 {
		t.Errorf("got (%d, %v), want (%d, true)", got, ok, epochSec*1000)
	}
}

func TestWriteSessionStartOnceCreatesFileIfAbsent(t *testing.T) {
	home := freshAgentHome(t)
	dir := filepath.Join(home, "brain-dev")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Unix()
	WriteSessionStartOnce("brain-dev")
	after := time.Now().Unix()

	file := filepath.Join(dir, "session-start")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("session-start not created: %v", err)
	}
	written, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		t.Fatalf("session-start not parseable: %v", err)
	}
	if written < before || written > after {
		t.Errorf("written=%d not within [%d, %d]", written, before, after)
	}
}

func TestWriteSessionStartOnceDoesNotOverwriteExisting(t *testing.T) {
	home := freshAgentHome(t)
	dir := filepath.Join(home, "brain-dev")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const old = "1720000000"
	file := filepath.Join(dir, "session-start")
	if err := os.WriteFile(file, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	WriteSessionStartOnce("brain-dev")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != old {
		t.Errorf("got %q, want unchanged %q", strings.TrimSpace(string(data)), old)
	}
}
