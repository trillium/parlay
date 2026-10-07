// Tests for drawdown.go (ticket B9, ported from packages/cli/src/commands/
// drawdown.ts, which has no dedicated TS test file — these cases were
// derived directly from reading the implementation).
package commands

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pinHandoffStore puts exactly one `handoff` on PATH — present when present is
// true, absent otherwise. Drawdown's closing recipe names `handoff create`, so
// the output depends on whether the author's federation store is installed;
// without pinning it, these tests would pass or fail based on the box.
func pinHandoffStore(t *testing.T, present bool) {
	t.Helper()
	dir := t.TempDir()
	if present {
		if err := os.WriteFile(filepath.Join(dir, "handoff"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func historyServer(t *testing.T, wantLimit string, rawJSON string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/history", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("limit"); wantLimit != "" && got != wantLimit {
			t.Errorf("history request limit=%q, want %q", got, wantLimit)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(rawJSON))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDrawdownDefaultLimitAndTemplate(t *testing.T) {
	srv := historyServer(t, "20", `[
		{"role":"human","text":"hi","ts":"2026-08-01T00:00:00Z"},
		{"role":"agent","text":"working on it","ts":"2026-08-01T00:00:01Z"}
	]`)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_ID", "agent-a")
	pinHandoffStore(t, true)

	out := captureStdout(t, func() { Drawdown(nil) })

	for _, want := range []string{
		"## Handoff —",
		"### What I was doing",
		"working on it",
		"### Recent context (last 2 message(s))",
		"### Next steps",
		"handoff create \"agent-a context handoff",
		"identity --submit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Drawdown() output missing %q\nfull output:\n%s", want, out)
		}
	}
}

// Fresh-clone case: no `handoff` store installed, so the closing recipe must
// not tell the reader to run `handoff create` — a command they do not have.
// The portable substitute (an explicit id) is named instead.
func TestDrawdownWithoutStoreDoesNotPrescribeHandoffCreate(t *testing.T) {
	srv := historyServer(t, "20", `[{"role":"agent","text":"working","ts":"2026-08-01T00:00:01Z"}]`)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_AGENT_ID", "agent-a")
	pinHandoffStore(t, false)

	out := captureStdout(t, func() { Drawdown(nil) })

	if strings.Contains(out, "handoff create") {
		t.Errorf("with no store installed, drawdown must not prescribe `handoff create`:\n%s", out)
	}
	for _, want := range []string{"No `handoff` store is installed here", "identity --submit <handoff-id>"} {
		if !strings.Contains(out, want) {
			t.Errorf("Drawdown() output missing %q\nfull output:\n%s", want, out)
		}
	}
	// The portable half of the artifact — the handoff body itself — is unchanged.
	for _, want := range []string{"## Handoff —", "### What I was doing", "working", "### Next steps"} {
		if !strings.Contains(out, want) {
			t.Errorf("Drawdown() output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestDrawdownExplicitN(t *testing.T) {
	srv := historyServer(t, "5", `[]`)
	t.Setenv("PARLAY_SERVER", srv.URL)

	out := captureStdout(t, func() { Drawdown([]string{"5"}) })
	if !strings.Contains(out, "(no messages)") {
		t.Errorf("Drawdown([5]) output = %q, want empty-body fallback", out)
	}
	if !strings.Contains(out, "(no agent messages in last 5)") {
		t.Errorf("Drawdown([5]) output = %q, want no-agent-messages summary fallback", out)
	}
}

func TestDrawdownInvalidNDies(t *testing.T) {
	for _, bad := range []string{"0", "-3", "notanumber"} {
		t.Run(bad, func(t *testing.T) {
			code, exited := withExitTrap(t, func() { Drawdown([]string{bad}) })
			if !exited || code != 2 {
				t.Errorf("Drawdown([%s]) exited=%v code=%d, want exit 2", bad, exited, code)
			}
		})
	}
}

func TestDrawdownNoAgentMessagesSummaryFallback(t *testing.T) {
	srv := historyServer(t, "20", `[{"role":"human","text":"only a human message","ts":"2026-08-01T00:00:00Z"}]`)
	t.Setenv("PARLAY_SERVER", srv.URL)

	out := captureStdout(t, func() { Drawdown(nil) })
	if !strings.Contains(out, "(no agent messages in last 20)") {
		t.Errorf("Drawdown() output = %q, want no-agent-messages fallback", out)
	}
}

func TestDrawdownSummaryTruncatedAndNewlinesCollapsed(t *testing.T) {
	longText := strings.Repeat("a", 350) + "\n\nmore\ntext"
	srv := historyServer(t, "20", fmt.Sprintf(`[{"role":"agent","text":%q,"ts":"2026-08-01T00:00:00Z"}]`, longText))
	t.Setenv("PARLAY_SERVER", srv.URL)

	out := captureStdout(t, func() { Drawdown(nil) })
	if strings.Contains(out, "more\ntext") {
		t.Errorf("Drawdown() output = %q, want collapsed newlines in summary", out)
	}
	if strings.Contains(out, strings.Repeat("a", 350)) {
		t.Errorf("Drawdown() output contains untruncated 350-char summary")
	}
}
