// FakeTalon is the test double for TalonAdapter: scripted focus state,
// recorded inserts, injectable failures. The live REPL never runs in CI.
package remoteinput

import (
	"errors"
	"sync"
)

// FakeTalon records what the service asked Talon to do.
type FakeTalon struct {
	mu sync.Mutex

	// Scripted visible state.
	ActiveAppName string
	WindowTitle   string

	// Injected failures.
	FocusAppErr error
	InsertErr   error

	// StickyActive disables FocusApp's cooperative effect (the named app
	// becoming active), simulating a focus request the OS did not honor.
	StickyActive bool

	// StickyWindow disables FocusWindow's cooperative effect, leaving
	// the focused title as scripted (e.g. empty when nothing focused).
	StickyWindow bool

	// Recorded calls, in order.
	FocusAppCalls    []string
	FocusWindowCalls []string
	Inserts          []string
	ActiveAppReads   int
}

// Verify FakeTalon implements TalonAdapter at compile time.
var _ TalonAdapter = (*FakeTalon)(nil)

// ErrFocusTransport is a canned transport failure for tests.
var ErrFocusTransport = errors.New("focus transport failed")

// ErrInsertTransport is a canned transport failure for tests.
var ErrInsertTransport = errors.New("insert transport failed")

// FocusApp records the call and applies the scripted effect: on success
// the named app becomes active (mirrors a cooperative target).
func (f *FakeTalon) FocusApp(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.FocusAppCalls = append(f.FocusAppCalls, name)
	if f.FocusAppErr != nil {
		return f.FocusAppErr
	}
	if !f.StickyActive {
		f.ActiveAppName = name
	}
	return nil
}

// FocusWindow records the call and applies the scripted effect.
func (f *FakeTalon) FocusWindow(title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.FocusWindowCalls = append(f.FocusWindowCalls, title)
	if f.FocusAppErr != nil {
		return f.FocusAppErr
	}
	if !f.StickyWindow {
		f.WindowTitle = title
	}
	return nil
}

// ActiveApp returns the scripted active application name.
func (f *FakeTalon) ActiveApp() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ActiveAppReads++
	if f.FocusAppErr != nil {
		return "", f.FocusAppErr
	}
	return f.ActiveAppName, nil
}

// FocusedWindowTitle returns the scripted focused window title.
func (f *FakeTalon) FocusedWindowTitle() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FocusAppErr != nil {
		return "", f.FocusAppErr
	}
	return f.WindowTitle, nil
}

// Insert records the text byte-for-byte.
func (f *FakeTalon) Insert(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.InsertErr != nil {
		return f.InsertErr
	}
	f.Inserts = append(f.Inserts, text)
	return nil
}
