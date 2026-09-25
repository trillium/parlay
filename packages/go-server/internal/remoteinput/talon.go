// Talon adapter seam: the only file that knows how Talon is reached.
//
// Contract recorded from a live check on this Mac (2026-09-25), not from
// the task brief:
//   - transport: Talon Python REPL at ~/.talon/.venv/bin/repl
//     (TALON_REPL_PATH overrides), Python source on stdin, result/print
//     output on stdout; CLI wrapper `bun run ~/.talon/talon_mcp/tools/cli.ts
//     repl "<python>"` / `status` verified working, Talon running.
//   - actions.insert / actions.key both callable (True, True).
//   - ui.apps() / ui.active_app() / ui.windows() all live.
//   - App.focus() and Window.focus() exist (dir() probe); ui.focused_element()
//     exists for the no-target sanity read.
//   - expression/print round-trip verified: `1+1` → `2`,
//     `print("hi-direct")` → `hi-direct`.
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

// runSnippet executes one Python snippet and returns its stdout.
func (t *REPLTalon) runSnippet(code string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.path)
	cmd.Stdin = strings.NewReader(code)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("talon repl: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// FocusApp asks the named app to take focus (case-insensitive match).
func (t *REPLTalon) FocusApp(name string) error {
	q, _ := json.Marshal(strings.ToLower(name))
	_, err := t.runSnippet(fmt.Sprintf(`[a.focus() for a in ui.apps() if a.name.lower() == %s]`, string(q)))
	return err
}

// FocusWindow asks the first window with a matching title to take focus.
func (t *REPLTalon) FocusWindow(title string) error {
	q, _ := json.Marshal(strings.ToLower(title))
	_, err := t.runSnippet(fmt.Sprintf(`[w.focus() for w in ui.windows() if %s in w.title.lower()]`, string(q)))
	return err
}

// ActiveApp returns the currently active application name (verify leg).
func (t *REPLTalon) ActiveApp() (string, error) {
	return t.runSnippet(`ui.active_app().name`)
}

// FocusedWindowTitle returns the focused window's title (verify leg).
func (t *REPLTalon) FocusedWindowTitle() (string, error) {
	return t.runSnippet(`(ui.focused_element() and ui.focused_element().window.title) or ""`)
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
