// App launch for the PWA's launchable-apps row (task-2cp06): an app Talon
// lists with no windows (hasWindows=false) can be started from the phone.
//
// POST /api/chat/remote-input/launch {device, app, dryRun} validates app
// against the names GET targets serves, starts it, then makes ONE
// best-effort focus of its first window through the same FocusApp +
// ActiveApp verification the inject path uses. It never injects text.
// Talon has no launch action, so the launch itself is `open -a <app>`
// behind the AppLauncher seam, which FakeLauncher replaces in tests.
//
// Phone-side, the app moving into the active-app row is observed on the
// existing targets poll; this response only reports what the server did.
package remoteinput

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sync"
)

// Launch statuses. All three are 2xx outcomes; a failed launch is an error.
const (
	// StatusLaunched: the app was started. Focus was attempted but not
	// verified (the window often does not exist yet) — still a success.
	StatusLaunched = "launched"
	// StatusLaunchFocused: the app was started and Talon verified it active.
	StatusLaunchFocused = "focused"
	// StatusLaunchDryRun is dry_run_passed: the request validated and
	// nothing was launched or focused.
	StatusLaunchDryRun = StatusDryRunPassed
)

// ErrUnknownApp means app is not one of the names GET targets reports.
var ErrUnknownApp = errors.New("unknown app")

// ErrLaunchFailed wraps any failure to start the application.
var ErrLaunchFailed = errors.New("launch failed")

// LaunchRequest is the POST /api/chat/remote-input/launch wire shape.
type LaunchRequest struct {
	Device string `json:"device"`
	App    string `json:"app"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// LaunchResponse is the 2xx answer.
type LaunchResponse struct {
	Device string `json:"device"`
	// App is the canonical Talon name, not the caller's spelling.
	App    string `json:"app"`
	Status string `json:"status"`
	DryRun bool   `json:"dryRun,omitempty"`
	// WouldRun names the command a dry run would have executed.
	WouldRun string `json:"wouldRun,omitempty"`
}

// AppLauncher starts an application by its Talon name.
type AppLauncher interface {
	Launch(app string) error
}

// ExecLauncher launches via `open -a <app>` on macOS.
type ExecLauncher struct{}

// NewExecLauncher builds the production launcher.
func NewExecLauncher() *ExecLauncher { return &ExecLauncher{} }

// Launch runs `open -a <app>`. app is one argv element, never a shell string.
func (ExecLauncher) Launch(app string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("launching apps is only supported on macOS (running on %s)", runtime.GOOS)
	}
	if out, err := exec.Command("open", "-a", app).CombinedOutput(); err != nil {
		return fmt.Errorf("open -a %q: %w: %s", app, err, string(out))
	}
	return nil
}

// launchCommand is the human-readable form reported by dry runs.
func launchCommand(app string) string { return "open -a " + app }

// FakeLauncher records launches; Err injects a failure.
type FakeLauncher struct {
	mu    sync.Mutex
	Err   error
	Calls []string
}

var _ AppLauncher = (*FakeLauncher)(nil)

// Launch records the call, then fails if Err is set.
func (f *FakeLauncher) Launch(app string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, app)
	return f.Err
}

// SetLauncher swaps the launch backend (tests inject FakeLauncher).
func (s *Service) SetLauncher(l AppLauncher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.launcher = l
}

// Launch validates and starts req.App. It is synchronous and does not use
// the injection queue. Errors wrap ErrUnknownApp, ErrLaunchFailed, or a
// targets-read failure (neither sentinel).
func (s *Service) Launch(req LaunchRequest) (LaunchResponse, error) {
	targets, err := s.talon.Targets()
	if err != nil {
		return LaunchResponse{}, fmt.Errorf("talon targets: %w", err)
	}
	app := ""
	for _, t := range targets {
		if matchName(t.Name, req.App) {
			app = t.Name
			break
		}
	}
	if app == "" {
		return LaunchResponse{}, fmt.Errorf("%w %q: see GET remote-input/targets for known names", ErrUnknownApp, req.App)
	}
	resp := LaunchResponse{Device: req.Device, App: app}
	if req.DryRun {
		resp.Status, resp.DryRun, resp.WouldRun = StatusLaunchDryRun, true, launchCommand(app)
		return resp, nil
	}
	s.mu.Lock()
	l := s.launcher
	if l == nil {
		l = NewExecLauncher()
		s.launcher = l
	}
	s.mu.Unlock()
	if err := l.Launch(app); err != nil {
		return LaunchResponse{}, fmt.Errorf("%w: %v", ErrLaunchFailed, err)
	}
	resp.Status = StatusLaunched
	if s.focusLaunched(app) {
		resp.Status = StatusLaunchFocused
	}
	return resp, nil
}

// focusLaunched makes one best-effort focus attempt and reports whether
// Talon verified the app active. Every failure is swallowed: the launch
// already succeeded and nothing is typed.
func (s *Service) focusLaunched(app string) bool {
	if err := s.talon.FocusApp(app); err != nil {
		return false
	}
	sleepSettle(s.settleDelay)
	active, err := s.talon.ActiveApp()
	return err == nil && matchName(active, app)
}
