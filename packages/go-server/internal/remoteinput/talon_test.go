// Adapter read tests: the Talon REPL echoes expression values as Python
// repr (`'Name'` with quotes) and current releases write every result to
// stderr, so the adapter must print-explicitly, merge both streams, and
// strip one repr-quote layer. Tests drive REPLTalon against mock repl
// scripts — the live REPL never runs in CI.
package remoteinput

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mockRepl writes an executable shell script that consumes stdin (like
// the real repl) and prints exactly out, then points a REPLTalon at it.
func mockRepl(t *testing.T, out string) *REPLTalon {
	t.Helper()
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s' " + shellQuote(out) + "\n"
	path := filepath.Join(t.TempDir(), "repl")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write mock repl: %v", err)
	}
	return &REPLTalon{path: path, timeout: 5 * time.Second}
}

// shellQuote wraps s in single quotes for the mock script body.
func shellQuote(s string) string {
	quoted := "'"
	for _, r := range s {
		if r == '\'' {
			quoted += "'\\''"
		} else {
			quoted += string(r)
		}
	}
	return quoted + "'"
}

func TestCleanReplString(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`'Google Chrome'`, `Google Chrome`},
		{`"Google Chrome"`, `Google Chrome`},
		{`Google Chrome`, `Google Chrome`},
		{`  ' spaced '  `, `spaced`},
		{`'it's quoted'`, `it's quoted`},
		{`it's`, `it's`},
		{`'a'`, `a`},
		{`''`, ``},
		{``, ``},
	} {
		if got := cleanReplString(tc.in); got != tc.want {
			t.Errorf("cleanReplString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestActiveAppStripsReprEcho(t *testing.T) {
	// An echoing REPL returning the repr-quoted `'WezTerm'` must still
	// yield the bare name after the fix.
	talon := mockRepl(t, "'WezTerm'\n")
	got, err := talon.ActiveApp()
	if err != nil {
		t.Fatalf("ActiveApp: %v", err)
	}
	if got != "WezTerm" {
		t.Fatalf("ActiveApp = %q, want %q", got, "WezTerm")
	}
}

func TestFocusedWindowTitleStripsReprEcho(t *testing.T) {
	talon := mockRepl(t, "'notes - scratch'\n")
	got, err := talon.FocusedWindowTitle()
	if err != nil {
		t.Fatalf("FocusedWindowTitle: %v", err)
	}
	if got != "notes - scratch" {
		t.Fatalf("FocusedWindowTitle = %q, want %q", got, "notes - scratch")
	}
}

func TestRunSnippetReadsStderr(t *testing.T) {
	// Current repl.py writes every result to stderr; a stdout-only
	// read returns "" and silently breaks verification.
	path := filepath.Join(t.TempDir(), "repl")
	script := "#!/bin/sh\ncat >/dev/null\necho 'hi-direct' >&2\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write mock repl: %v", err)
	}
	talon := &REPLTalon{path: path, timeout: 5 * time.Second}
	got, err := talon.runSnippet(`print("hi-direct")`)
	if err != nil {
		t.Fatalf("runSnippet: %v", err)
	}
	if got != "hi-direct" {
		t.Fatalf("runSnippet = %q, want %q", got, "hi-direct")
	}
}
