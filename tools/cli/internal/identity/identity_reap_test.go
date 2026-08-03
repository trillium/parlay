// Integration tests for `parlay identity --reap-ephemeral`: GC of ephemeral
// agents idle past the window, --dry preview, and non-ephemeral immunity.
//
// Mirrors packages/cli/src/commands-identity-reap.test.ts.
package identity

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// age backdates a store's identity.md so it falls outside the reap window.
func age(t *testing.T, dir string, hoursAgo float64) {
	t.Helper()
	when := time.Now().Add(-time.Duration(hoursAgo * float64(time.Hour)))
	if err := os.Chtimes(filepath.Join(dir, "identity.md"), when, when); err != nil {
		t.Fatal(err)
	}
}

func TestReapEphemeralDryListsWithoutDeleting(t *testing.T) {
	home := freshHome(t)
	age(t, seedAgent(t, home, "eph-11111111", seedOpts{Ephemeral: true}), 48)
	seedAgent(t, home, "eph-22222222", seedOpts{Ephemeral: true}) // fresh — kept
	seedAgent(t, home, "durable", seedOpts{})                     // non-ephemeral — never reaped

	captureStdout(t, func() {
		CmdIdentity([]string{"--reap-ephemeral", "--dry"})
	})

	for _, id := range []string{"eph-11111111", "eph-22222222", "durable"} {
		if _, err := os.Stat(filepath.Join(home, id)); err != nil {
			t.Errorf("%s should still exist after --dry: %v", id, err)
		}
	}
}

func TestReapEphemeralDeletesStaleKeepsFreshAndNonEphemeral(t *testing.T) {
	home := freshHome(t)
	age(t, seedAgent(t, home, "eph-33333333", seedOpts{Ephemeral: true}), 100) // stale
	seedAgent(t, home, "eph-44444444", seedOpts{Ephemeral: true})              // fresh
	age(t, seedAgent(t, home, "durable-agent", seedOpts{}), 100)               // non-ephemeral, old

	captureStdout(t, func() {
		CmdIdentity([]string{"--reap-ephemeral", "--older-than", "24h"})
	})

	if _, err := os.Stat(filepath.Join(home, "eph-33333333")); err == nil {
		t.Error("eph-33333333 should have been reaped")
	}
	if _, err := os.Stat(filepath.Join(home, "eph-44444444")); err != nil {
		t.Error("eph-44444444 (fresh) should be kept")
	}
	if _, err := os.Stat(filepath.Join(home, "durable-agent")); err != nil {
		t.Error("durable-agent (non-ephemeral) should be kept")
	}
}

func TestReapEphemeralHonorsCustomOlderThan(t *testing.T) {
	home := freshHome(t)
	age(t, seedAgent(t, home, "eph-55555555", seedOpts{Ephemeral: true}), 2) // 2h old

	captureStdout(t, func() {
		CmdIdentity([]string{"--reap-ephemeral", "--older-than", "1h"})
	})

	if _, err := os.Stat(filepath.Join(home, "eph-55555555")); err == nil {
		t.Error("eph-55555555 should have been reaped under a 1h window")
	}
}

func TestReapEphemeralRejectsMalformedOlderThan(t *testing.T) {
	home := freshHome(t)
	seedAgent(t, home, "eph-66666666", seedOpts{Ephemeral: true})

	_, code, exited := runCapturingExit(t, func() {
		CmdIdentity([]string{"--reap-ephemeral", "--older-than", "soon"})
	})
	if !exited || code != 2 {
		t.Fatalf("exited=%v code=%d, want exited with 2", exited, code)
	}
	if _, err := os.Stat(filepath.Join(home, "eph-66666666")); err != nil {
		t.Error("nothing should have been deleted on error")
	}
}
