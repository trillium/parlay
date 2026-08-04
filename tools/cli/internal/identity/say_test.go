// Covers `parlay say`/`parlay reply` (CmdSay), including its wiring to the
// create->submit death-window guard (internal/sayguard, itself backed by
// internal/resolvehandoff). Ported intent from
// packages/cli/src/commands-identity/say.ts — the TS source has no
// dedicated say.test.ts (only say-guard.test.ts / resolve-handoff.test.ts),
// so this is new coverage added alongside wiring CmdSay into main.go (B8).
package identity

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// stubHandoffStore installs a fake `handoff` executable on PATH for the
// duration of the test, whose `list` subcommand always returns listJSON.
// Mirrors resolvehandoff_test.go's stubStore for the single case CmdSay's
// guard integration needs.
func stubHandoffStore(t *testing.T, listJSON string) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n  list) printf '%%s' %s; exit 0;;\n  *) exit 3;;\nesac\n", shQuote(listJSON))
	if err := os.WriteFile(filepath.Join(dir, "handoff"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestSayDiesWhenNoAgentIdentity(t *testing.T) {
	t.Setenv("PARLAY_AGENT_ID", "")
	trapExit(t)
	var code int
	var exited bool
	captureStdout(t, func() {
		code, exited = testsupport.Capture(func() { CmdSay([]string{"hello"}) })
	})
	if !exited || code != config.ExitUsage {
		t.Errorf("got (code=%d exited=%v), want (%d, true)", code, exited, config.ExitUsage)
	}
}

func TestSayResolvesAgentFromFlagAndPostsReply(t *testing.T) {
	h := startHarness(t)
	freshHome(t) // fresh PARLAY_AGENT_HOME, no identity.md, no session-start
	// No `handoff` stub on PATH: resolveRow fails closed, so the guard finds
	// nothing open and stays silent — this is the ordinary, unremarkable send.
	out := captureStdout(t, func() {
		CmdSay([]string{"--agent", "mayor", "hello", "world"})
	})
	if !strings.Contains(out, "said as mayor") || !strings.Contains(out, "reply-1") {
		t.Errorf("stdout = %q, want confirmation for mayor/reply-1", out)
	}
	if len(h.replyBodies) != 1 {
		t.Fatalf("got %d reply POSTs, want 1", len(h.replyBodies))
	}
	body := h.replyBodies[0]
	if body["text"] != "hello world" || body["agent"] != "mayor" {
		t.Errorf("got body %+v, want text=hello world agent=mayor", body)
	}
}

func TestSayResolvesAgentFromEnv(t *testing.T) {
	h := startHarness(t)
	freshHome(t)
	t.Setenv("PARLAY_AGENT_ID", "brain-dev")
	captureStdout(t, func() {
		CmdSay([]string{"hi"})
	})
	if len(h.replyBodies) != 1 || h.replyBodies[0]["agent"] != "brain-dev" {
		t.Errorf("got replyBodies=%+v, want one body with agent=brain-dev", h.replyBodies)
	}
}

func TestSayWarnsOnUnsubmittedCurrentSessionHandoff(t *testing.T) {
	startHarness(t)
	freshHome(t) // no identity.md -> no pinned pointer
	stubHandoffStore(t, `[{"id":"handoff-live","status":"open","created":"2099-01-01T00:00:00Z"}]`)

	var out, errOut string
	errOut = captureStderr(t, func() {
		out = captureStdout(t, func() {
			CmdSay([]string{"--agent", "mayor", "shutting down"})
		})
	})
	if !strings.Contains(errOut, "⚠️") || !strings.Contains(errOut, "handoff-live") || !strings.Contains(errOut, "identity --submit") {
		t.Errorf("stderr = %q, want aggressive unsubmitted-handoff warning", errOut)
	}
	if !strings.Contains(out, "said as mayor") {
		t.Errorf("stdout = %q, want the send to still succeed (warn-only, never blocking)", out)
	}
}

func TestSayWarnsGentlyForInheritedHandoff(t *testing.T) {
	startHarness(t)
	freshHome(t) // no identity.md -> no pinned pointer
	stubHandoffStore(t, `[{"id":"handoff-stale","status":"open","created":"2000-01-01T00:00:00Z"}]`)

	errOut := captureStderr(t, func() {
		captureStdout(t, func() {
			CmdSay([]string{"--agent", "mayor", "hello"})
		})
	})
	if !strings.Contains(errOut, "💡") || !strings.Contains(errOut, "--dismiss-handoff") {
		t.Errorf("stderr = %q, want gentle inherited-handoff warning", errOut)
	}
	if strings.Contains(errOut, "⚠️") {
		t.Errorf("stderr = %q, must not show the aggressive current-session nag for an inherited handoff", errOut)
	}
}

func TestSayNoWarningWhenHandoffAlreadyPinned(t *testing.T) {
	startHarness(t)
	home := freshHome(t)
	if err := os.MkdirAll(filepath.Join(home, "mayor"), 0o755); err != nil {
		t.Fatal(err)
	}
	pinned := "---\nid: mayor\n---\n# Identity — mayor\n\n> 📎 Handoff: handoff-live — run `handoff show handoff-live` for full session state\n"
	if err := os.WriteFile(filepath.Join(home, "mayor", "identity.md"), []byte(pinned), 0o644); err != nil {
		t.Fatal(err)
	}
	stubHandoffStore(t, `[{"id":"handoff-live","status":"open","created":"2099-01-01T00:00:00Z"}]`)

	errOut := captureStderr(t, func() {
		captureStdout(t, func() {
			CmdSay([]string{"--agent", "mayor", "all good"})
		})
	})
	if errOut != "" {
		t.Errorf("stderr = %q, want no warning once the handoff is pinned", errOut)
	}
}
