// Target enumeration from Talon (task-46ys9): a phone UI cannot list
// targets from OS processes — the macOS process name (e.g. wezterm-gui)
// is not the Talon app name (WezTerm) that focus verification matches.
// GET /api/chat/remote-input/targets therefore serves Talon's own names
// so a picked target round-trips through verification by construction.
//
// One bounded subprocess call per Targets invocation; the settle delay
// stays in our process and Talon's main thread is never polled
// (brain-15l95). The read is an explicit print (a bare expression echoes
// Python repr) and a single statement (the repl compiles ONE statement).
package remoteinput

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Target is one injectable application as Talon sees it, in Talon's own
// ui.apps() ordering (the spec's cycling/recently-used behaviour needs
// the computer's ordering, not ours).
type Target struct {
	// Name is the exact Talon ui.apps() name — the same string focus
	// verification compares against, so it round-trips by construction.
	Name string `json:"name"`
	// Focused reports whether Talon names this app currently active.
	Focused bool `json:"focused"`
	// WindowTitle is the relevant window title: the first window in
	// Talon's per-app ordering (empty when the app has no windows).
	WindowTitle string `json:"windowTitle,omitempty"`
	// WindowCount is the open-window count for this app.
	WindowCount int `json:"windowCount"`
	// HasWindows distinguishes apps with windows from launchable apps
	// without any (the launchable-apps row needs them listed too).
	HasWindows bool `json:"hasWindows"`
}

// TargetsResponse is the GET targets wire shape. Targets is read-only —
// no focus request, no keystroke — so dryRun changes nothing about what
// it does; the flag is echoed only so a caller can see the mode it used.
type TargetsResponse struct {
	Targets []Target `json:"targets"`
	DryRun  bool     `json:"dryRun,omitempty"`
}

// talonTargetsWire is the JSON the Talon snippet prints.
type talonTargetsWire struct {
	Active string `json:"active"`
	Apps   []struct {
		Name    string   `json:"name"`
		Windows []string `json:"windows"`
	} `json:"apps"`
}

// targetsSnippet is the one-statement Talon program behind Targets: a
// single print of a JSON dump over ui.apps() with each app's window
// titles plus the active app name. Pure function so tests prove the
// single-statement shape without a live Talon.
func targetsSnippet() string {
	return `print(__import__("json").dumps({"active": (ui.active_app().name if ui.active_app() else ""), "apps": [{"name": a.name, "windows": [w.title for w in a.windows()]} for a in ui.apps()]}))`
}

// parseTargetsJSON converts one repl readout into Targets, preserving
// Talon's ordering. Focused is an exact case-insensitive match against
// the reported active name (the same matchName verification uses).
func parseTargetsJSON(out string) ([]Target, error) {
	var wire talonTargetsWire
	dec := json.NewDecoder(strings.NewReader(out))
	if err := dec.Decode(&wire); err != nil {
		return nil, fmt.Errorf("talon targets: bad JSON: %w", err)
	}
	targets := make([]Target, 0, len(wire.Apps))
	for _, app := range wire.Apps {
		t := Target{
			Name:        app.Name,
			Focused:     matchName(app.Name, wire.Active),
			WindowCount: len(app.Windows),
			HasWindows:  len(app.Windows) > 0,
		}
		if len(app.Windows) > 0 {
			t.WindowTitle = app.Windows[0]
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// Targets asks Talon for its application list: one bounded repl call,
// read-only (no focus change, no keystroke), so it is dry-run safe by
// construction.
func (t *REPLTalon) Targets() ([]Target, error) {
	out, err := t.runSnippet(targetsSnippet())
	if err != nil {
		return nil, err
	}
	return parseTargetsJSON(out)
}

// Targets serves the read-only application list through the service so
// the handler stays a thin wire adapter. It never queues and never
// touches the injection pipeline.
func (s *Service) Targets() ([]Target, error) {
	return s.talon.Targets()
}
