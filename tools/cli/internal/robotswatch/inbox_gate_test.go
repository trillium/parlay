// Tests for the inbox dispatch kill switch and router: gate ON/OFF via
// sentinel and env, and inbox:created routing to the inbox dispatcher.
package robotswatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeInboxSentinel creates the inbox-dispatch.off sentinel inside the given
// state dir (mirrors inboxDispatchSentinelPath).
func writeInboxSentinel(t *testing.T, stateDir string) string {
	t.Helper()
	path := filepath.Join(stateDir, "inbox-dispatch.off")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("writeInboxSentinel: %v", err)
	}
	return path
}

func TestDispatchInboxGateOnAttemptsSpawn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_INBOX_DISPATCH", "") // ensure env is neutral

	out := captureStderr(t, func() { dispatchInbox("inbox-aaa", false) })
	// inbox-dispatch binary won't exist in tests; we just need proof the
	// gate didn't block it — either "not runnable" or "exited N" lands.
	if strings.Contains(out, "inbox dispatch is OFF") {
		t.Fatalf("gate should be ON but got: %s", out)
	}
}

func TestDispatchInboxGateOffViaSentinelSkips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_INBOX_DISPATCH", "") // no env override
	writeInboxSentinel(t, dir)

	out := captureStderr(t, func() { dispatchInbox("inbox-bbb", false) })
	if !strings.Contains(out, "inbox dispatch is OFF") {
		t.Fatalf("expected OFF skip line, got: %q", out)
	}
	if !strings.Contains(out, "inbox-bbb") {
		t.Fatalf("expected id in skip line, got: %q", out)
	}
}

func TestDispatchInboxGateOffViaEnvSkips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_INBOX_DISPATCH", "off")
	// no sentinel — env alone is enough

	out := captureStderr(t, func() { dispatchInbox("inbox-ccc", false) })
	if !strings.Contains(out, "inbox dispatch is OFF") {
		t.Fatalf("expected OFF skip line, got: %q", out)
	}
}

func TestDispatchInboxEnvOnDoesNotOverrideSentinel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_INBOX_DISPATCH", "on") // env=on must NOT override sentinel
	writeInboxSentinel(t, dir)

	out := captureStderr(t, func() { dispatchInbox("inbox-ddd", false) })
	if !strings.Contains(out, "inbox dispatch is OFF") {
		t.Fatalf("sentinel must win over env=on, got: %q", out)
	}
}

func TestInboxDispatchOffPrecedence(t *testing.T) {
	cases := []struct {
		name        string
		envVal      string
		hasSentinel bool
		wantOff     bool
	}{
		{"no_env_no_sentinel", "", false, false},
		{"env_on_no_sentinel", "on", false, false},
		{"env_off_no_sentinel", "off", false, true},
		{"no_env_sentinel", "", true, true},
		{"env_on_sentinel", "on", true, true}, // sentinel wins over env=on
		{"env_off_sentinel", "off", true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PARLAY_STATE_HOME", dir)
			t.Setenv("PARLAY_INBOX_DISPATCH", tc.envVal)
			if tc.hasSentinel {
				writeInboxSentinel(t, dir)
			}
			got := inboxDispatchOff()
			if got != tc.wantOff {
				t.Fatalf("inboxDispatchOff() = %v, want %v", got, tc.wantOff)
			}
		})
	}
}

// TestRouteInboxCreatedDispatches verifies the router maps inbox:created to
// the inbox dispatcher (and not to the mechanic or the closed handler).
func TestRouteInboxCreatedDispatches(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_INBOX_DISPATCH", "")
	t.Setenv("PARLAY_MECHANIC_DISPATCH", "")

	bead := Bead{ID: "inbox-eee", Status: "open", Title: "Prove it"}
	out := captureStderr(t, func() {
		routeEvent(RouteEvent{Store: "inbox", Kind: EventCreated, ID: "inbox-eee"}, bead, false)
	})
	if strings.Contains(out, "mechanic dispatch is OFF") || strings.Contains(out, "dispatched mechanic") {
		t.Fatalf("inbox:created must not route to mechanic-dispatch, got: %q", out)
	}
	if !strings.Contains(out, "inbox-eee") {
		t.Fatalf("expected inbox-eee in dispatch output, got: %q", out)
	}
}

// TestInboxWatchRegistered verifies the poll table subscribes to inbox
// creation and closure transitions — a new row here, not new machinery.
func TestInboxWatchRegistered(t *testing.T) {
	var foundCreated, foundClosed bool
	for _, w := range watches {
		if w.Store != "inbox" {
			continue
		}
		for _, k := range w.Kinds {
			switch k {
			case EventCreated:
				foundCreated = true
			case EventClosed:
				foundClosed = true
			}
		}
	}
	if !foundCreated {
		t.Fatalf("watches has no inbox:created subscription: %+v", watches)
	}
	if !foundClosed {
		t.Fatalf("watches has no inbox:closed subscription: %+v", watches)
	}
}
