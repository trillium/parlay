// Tests for `parlay inbox-dispatch on|off|status`.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInboxDispatchOffCreatesSentinel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)

	out := captureStdout(t, func() { InboxDispatch([]string{"off"}) })
	if !strings.Contains(out, "OFF") {
		t.Fatalf("expected OFF in output, got: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "inbox-dispatch.off")); err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}
}

func TestInboxDispatchOnRemovesSentinel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	sentinelPath := filepath.Join(dir, "inbox-dispatch.off")
	if err := os.WriteFile(sentinelPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { InboxDispatch([]string{"on"}) })
	if !strings.Contains(out, "ON") {
		t.Fatalf("expected ON in output, got: %q", out)
	}
	if _, err := os.Stat(sentinelPath); !os.IsNotExist(err) {
		t.Fatalf("sentinel still present after inbox-dispatch on")
	}
}

func TestInboxDispatchOnIdempotentWhenNoSentinel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	// no sentinel — should succeed without error
	out := captureStdout(t, func() { InboxDispatch([]string{"on"}) })
	if !strings.Contains(out, "ON") {
		t.Fatalf("expected ON, got: %q", out)
	}
}

func TestInboxDispatchStatusOn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)

	out := captureStdout(t, func() { InboxDispatch([]string{"status"}) })
	if !strings.Contains(out, "on") {
		t.Fatalf("expected on status, got: %q", out)
	}
}

func TestInboxDispatchStatusOffViaSentinel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "inbox-dispatch.off"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { InboxDispatch([]string{"status"}) })
	if !strings.Contains(out, "off") {
		t.Fatalf("expected off status, got: %q", out)
	}
}
