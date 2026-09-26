// Talon adapter seam: the only file that knows how Talon is reached.
//
// Contract recorded from a live check on this Mac (2026-09-25), not from
// the task brief:
//   - transport: Talon Python REPL at ~/.talon/.venv/bin/repl
//     (TALON_REPL_PATH overrides), Python source on stdin, result/print
//     output on stdout OR stderr depending on the Talon release — the
//     bundled repl.py at 2026-09-25 writes every result to stderr, so
//     runSnippet merges both streams (see remote-input verify fix).
//     CLI wrapper `bun run ~/.talon/talon_mcp/tools/cli.ts repl "<python>"`
//     currently returns empty output for every call because it captures
//     stdout only; verify adapter claims via the raw repl, not that CLI.
//   - actions.insert / actions.key both callable (True, True).
//   - ui.apps() / ui.active_app() / ui.windows() all live.
//   - App.focus() and Window.focus() exist (dir() probe); ui.focused_element()
//     exists for the no-target sanity read.
//   - expression/print round-trip verified: `1+1` → `2`,
//     `print("hi-direct")` → `hi-direct` (both on stderr now).
//
// Main-thread discipline (brain-15l95): the command_client defect was a
// busy-wait loop ON Talon's serial main thread. This adapter never does
// that — each method is one bounded subprocess call that runs a single
// instant Talon action and returns. Any settle delay (e.g. after focus)
// sleeps in OUR process (service.go), never as actions.sleep polling on
// Talon's thread.
package remoteinput

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// replTimeout bounds any single Talon round-trip. Talon executes one
// instant action per call, so this is a transport guard, not a poll budget.
const replTimeout = 10 * time.Second

// TalonAdapter is the seam every injection goes through. The service
// orchestrates (focus → verify → insert); implementations only drive.
type TalonAdapter interface {
	FocusApp(name string) error
	FocusWindow(title string) error
	ActiveApp() (string, error)
	FocusedWindowTitle() (string, error)
	Insert(text string) error
	// Targets lists Talon's applications in Talon's own ordering.
	// Read-only: no focus change, no keystroke (dry-run safe).
	Targets() ([]Target, error)
}

// replPath mirrors ~/.talon/talon_mcp/tools/lib/repl.ts getReplPath.
func replPath() string {
	if p := os.Getenv("TALON_REPL_PATH"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.ExpandEnv("$HOME"), ".talon", ".venv", "bin", "repl")
	}
	return filepath.Join(home, ".talon", ".venv", "bin", "repl")
}

// REPLTalon drives the live Talon REPL as a subprocess. Our process blocks
// on the subprocess exit; Talon's main thread only runs the one instant
// action inside the snippet.
type REPLTalon struct {
	path    string
	timeout time.Duration
}

// NewREPLTalon builds the live adapter (default REPL path + timeout).
func NewREPLTalon() *REPLTalon {
	return &REPLTalon{path: replPath(), timeout: replTimeout}
}

// runSnippet executes one Python snippet and returns its output. Stdout
// and stderr are merged: the bundled repl.py historically printed results
// on stdout, but the current release writes every result to stderr, so
// reading one stream only yields empty strings (this broke focus
// verification and emptied the talon_mcp CLI the same way).
func (t *REPLTalon) runSnippet(code string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.path)
	cmd.Stdin = strings.NewReader(code)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("talon repl: %w: %s", err, strings.TrimSpace(combined.String()))
	}
	return strings.TrimSpace(combined.String()), nil
}

// FocusApp asks the named app to take focus (case-insensitive match).
func (t *REPLTalon) FocusApp(name string) error {
	q, _ := json.Marshal(strings.ToLower(name))
	_, err := t.runSnippet(fmt.Sprintf(`[a.focus() for a in ui.apps() if a.name.lower() == %s]`, string(q)))
	return err
}

// FocusWindow asks the window with the exactly matching title (case-insensitive) to take focus.
func (t *REPLTalon) FocusWindow(title string) error {
	q, _ := json.Marshal(strings.ToLower(title))
	_, err := t.runSnippet(fmt.Sprintf(`[w.focus() for w in ui.windows() if w.title.lower() == %s]`, string(q)))
	return err
}

// ActiveApp returns the currently active application name (verify leg).
// The read is an explicit print so the REPL returns the bare name: a
// bare `ui.active_app().name` expression echoes its repr (`'Name'` with
// quotes), which never equals the requested target and failed every
// verification. cleanReplString strips one repr-quote layer as
// defense-in-depth for REPLs that still echo the repr.
func (t *REPLTalon) ActiveApp() (string, error) {
	out, err := t.runSnippet(`print(ui.active_app().name)`)
	if err != nil {
		return "", err
	}
	return cleanReplString(out), nil
}

// FocusedWindowTitle returns the focused window's title (verify leg).
// Same repr-echo hazard as ActiveApp: explicit print plus cleanReplString.
func (t *REPLTalon) FocusedWindowTitle() (string, error) {
	out, err := t.runSnippet(`print((ui.focused_element() and ui.focused_element().window.title) or "")`)
	if err != nil {
		return "", err
	}
	return cleanReplString(out), nil
}

// cleanReplString trims surrounding space and one layer of Python repr
// quoting so an echoing REPL returning `'Name'` still yields `Name`.
// Only a matched outer pair is stripped; inner quotes are untouched.
func cleanReplString(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	return s
}

// Insert injects text literally via Talon's insert action. The text is
// JSON-quoted into a Python string literal by insertSnippet, so multiline
// and quoting survive byte-for-byte; actions.insert performs no
// normalization and there is no large-paste split in MVP.
func (t *REPLTalon) Insert(text string) error {
	_, err := t.runSnippet(insertSnippet(text))
	return err
}

// insertSnippet builds the one-shot insert program. Pure function so tests
// prove the quoting without a live Talon.
func insertSnippet(text string) string {
	q, _ := json.Marshal(text)
	return fmt.Sprintf(`actions.insert(%s)`, string(q))
}
