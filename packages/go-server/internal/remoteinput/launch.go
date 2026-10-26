// App launch for the PWA's launchable-apps row (task-2cp06): an app Talon
// lists with no windows (hasWindows=false) can be started from the phone.
//
// POST /api/chat/remote-input/launch {device, app, dryRun} validates app
// against the names GET targets serves, starts it, then makes ONE
// best-effort focus of its first window through the same FocusApp +
// ActiveApp verification the inject path uses. It never injects text.
// Talon has no launch action, so the launch itself is `open -b <bundle>`
// (or `open -a <app>` when Talon reports no bundle id) behind the
// AppLauncher seam, which FakeLauncher replaces in tests. The launch and
// focus run on the service's FIFO worker so they cannot interleave with an
// in-flight inject's focus check and insert.
//
// Phone-side, the app moving into the active-app row is observed on the
// existing targets poll; this response only reports what the server did.
package remoteinput

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
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

// errServiceStopped is returned when the worker stops before a queued
// launch runs.
var errServiceStopped = errors.New("remote-input service stopped")

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

// AppLauncher starts an application GET targets listed.
type AppLauncher interface {
	Launch(t Target) error
}

// ExecLauncher launches via `open` on macOS. run is the exec seam; nil
// means the real `open` binary.
type ExecLauncher struct {
	run func(args ...string) error
}

// NewExecLauncher builds the production launcher.
func NewExecLauncher() *ExecLauncher { return &ExecLauncher{run: runOpen} }

// Launch runs `open` with openArgs(t); every value is one argv element,
// never a shell string.
func (l ExecLauncher) Launch(t Target) error {
	run := l.run
	if run == nil {
		run = runOpen
	}
	return run(openArgs(t)...)
}

func runOpen(args ...string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("launching apps is only supported on macOS (running on %s)", runtime.GOOS)
	}
	if out, err := exec.Command("open", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("open %q: %w: %s", args, err, string(out))
	}
	return nil
}

// openArgs launches by bundle id when Talon knows it, else by name.
func openArgs(t Target) []string {
	if t.Bundle != "" {
		return []string{"-b", t.Bundle}
	}
	return []string{"-a", t.Name}
}

// launchCommand is the human-readable form reported by dry runs.
func launchCommand(t Target) string { return "open " + strings.Join(openArgs(t), " ") }

// FakeLauncher records launches; Err injects a failure.
type FakeLauncher struct {
	mu    sync.Mutex
	Err   error
	Calls []Target
}

var _ AppLauncher = (*FakeLauncher)(nil)

// Launch records the call, then fails if Err is set.
func (f *FakeLauncher) Launch(t Target) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, t)
	return f.Err
}

// SetLauncher swaps the launch backend (tests inject FakeLauncher).
func (s *Service) SetLauncher(l AppLauncher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.launcher = l
}

// job is one FIFO worker item: a submission, or an app launch.
type job struct {
	sub    Submission
	launch *launchJob
}

type launchJob struct {
	target Target
	done   chan launchResult
}

type launchResult struct {
	focused bool
	err     error
}

// Launch validates and starts req.App, waiting for the worker to run it.
// Errors wrap ErrUnknownApp, ErrLaunchFailed, or a targets-read failure
// (neither sentinel).
func (s *Service) Launch(req LaunchRequest) (LaunchResponse, error) {
	targets, err := s.talon.Targets()
	if err != nil {
		return LaunchResponse{}, fmt.Errorf("talon targets: %w", err)
	}
	var target *Target
	for i := range targets {
		if matchName(targets[i].Name, req.App) {
			target = &targets[i]
			break
		}
	}
	if target == nil {
		return LaunchResponse{}, fmt.Errorf("%w %q: see GET remote-input/targets for known names", ErrUnknownApp, req.App)
	}
	resp := LaunchResponse{Device: req.Device, App: target.Name}
	if req.DryRun {
		resp.Status, resp.DryRun, resp.WouldRun = StatusLaunchDryRun, true, launchCommand(*target)
		return resp, nil
	}
	lj := &launchJob{target: *target, done: make(chan launchResult, 1)}
	s.mu.Lock()
	s.pending = append(s.pending, job{launch: lj})
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	var res launchResult
	select {
	case res = <-lj.done:
	case <-s.done:
		return LaunchResponse{}, errServiceStopped
	}
	if res.err != nil {
		return LaunchResponse{}, res.err
	}
	resp.Status = StatusLaunched
	if res.focused {
		resp.Status = StatusLaunchFocused
	}
	return resp, nil
}

// launchAndFocus runs on the worker: start the app, then make one
// best-effort focus attempt and report whether Talon verified it active.
// Focus failures are swallowed: the launch already succeeded and nothing
// is typed.
func (s *Service) launchAndFocus(lj *launchJob) launchResult {
	s.mu.Lock()
	l := s.launcher
	if l == nil {
		l = NewExecLauncher()
		s.launcher = l
	}
	s.mu.Unlock()
	if err := l.Launch(lj.target); err != nil {
		return launchResult{err: fmt.Errorf("%w: %v", ErrLaunchFailed, err)}
	}
	app := lj.target.Name
	if err := s.talon.FocusApp(app); err != nil {
		return launchResult{}
	}
	sleepSettle(s.settleDelay)
	active, err := s.talon.ActiveApp()
	return launchResult{focused: err == nil && matchName(active, app)}
}
