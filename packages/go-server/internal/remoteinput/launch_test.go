package remoteinput

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func launchSvc(t *testing.T, ft *FakeTalon, fl *FakeLauncher) *Service {
	t.Helper()
	s := NewService(ft, 0, nil)
	t.Cleanup(s.Stop)
	s.SetLauncher(fl)
	return s
}

func TestLaunchHappyPathVerifiedFocus(t *testing.T) {
	ft := &FakeTalon{TargetsList: []Target{{Name: "Safari"}, {Name: "Notes"}}}
	fl := &FakeLauncher{}
	resp, err := launchSvc(t, ft, fl).Launch(LaunchRequest{Device: "d", App: "notes"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusLaunchFocused || resp.App != "Notes" {
		t.Fatalf("resp = %+v", resp)
	}
	if len(fl.Calls) != 1 || fl.Calls[0].Name != "Notes" || len(ft.FocusAppCalls) != 1 {
		t.Fatalf("calls: launch=%v focus=%v", fl.Calls, ft.FocusAppCalls)
	}
	if len(ft.Inserts) != 0 || len(ft.FocusWindowCalls) != 0 {
		t.Fatal("launch must never insert text")
	}
}

func TestLaunchFocusFailureStillLaunched(t *testing.T) {
	cases := map[string]*FakeTalon{
		"focus errors":      {TargetsList: []Target{{Name: "Notes"}}, FocusAppErr: ErrFocusTransport},
		"focus not honored": {TargetsList: []Target{{Name: "Notes"}}, StickyActive: true, ActiveAppName: "Other"},
	}
	for name, ft := range cases {
		t.Run(name, func(t *testing.T) {
			fl := &FakeLauncher{}
			resp, err := launchSvc(t, ft, fl).Launch(LaunchRequest{Device: "d", App: "Notes"})
			if err != nil || resp.Status != StatusLaunched {
				t.Fatalf("resp=%+v err=%v, want launched", resp, err)
			}
			if len(fl.Calls) != 1 {
				t.Fatalf("launch calls = %v", fl.Calls)
			}
		})
	}
}

func TestLaunchDryRunPerformsNothing(t *testing.T) {
	ft := &FakeTalon{TargetsList: []Target{{Name: "Notes"}}}
	fl := &FakeLauncher{}
	resp, err := launchSvc(t, ft, fl).Launch(LaunchRequest{Device: "d", App: "Notes", DryRun: true})
	if err != nil || resp.Status != StatusDryRunPassed || !resp.DryRun || resp.WouldRun != "open -a Notes" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if len(fl.Calls)+len(ft.FocusAppCalls)+len(ft.Inserts) != 0 {
		t.Fatal("dry run performed an action")
	}
}

func TestLaunchUnknownAppAndFailure(t *testing.T) {
	ft := &FakeTalon{TargetsList: []Target{{Name: "Notes"}}}
	fl := &FakeLauncher{}
	s := launchSvc(t, ft, fl)
	if _, err := s.Launch(LaunchRequest{Device: "d", App: "Nope"}); !errors.Is(err, ErrUnknownApp) {
		t.Fatalf("err = %v, want ErrUnknownApp", err)
	}
	if len(fl.Calls) != 0 {
		t.Fatal("unknown app must not launch")
	}
	fl.Err = errors.New("boom")
	if _, err := s.Launch(LaunchRequest{Device: "d", App: "Notes"}); !errors.Is(err, ErrLaunchFailed) {
		t.Fatalf("err = %v, want ErrLaunchFailed", err)
	}
	if len(ft.FocusAppCalls) != 0 {
		t.Fatal("no focus after failed launch")
	}
}

func TestLaunchUsesBundleIDWhenTalonHasOne(t *testing.T) {
	cases := []struct {
		name   string
		target Target
		want   []string
	}{
		{"bundle present", Target{Name: "Code", Bundle: "com.microsoft.VSCode"}, []string{"-b", "com.microsoft.VSCode"}},
		{"bundle absent", Target{Name: "Notes"}, []string{"-a", "Notes"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			l := &ExecLauncher{run: func(args ...string) error { got = args; return nil }}
			s := NewService(&FakeTalon{TargetsList: []Target{tc.target}}, 0, nil)
			t.Cleanup(s.Stop)
			s.SetLauncher(l)
			if _, err := s.Launch(LaunchRequest{Device: "d", App: tc.target.Name}); err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("open args = %v, want %v", got, tc.want)
			}
			dry, err := s.Launch(LaunchRequest{Device: "d", App: tc.target.Name, DryRun: true})
			if err != nil || dry.WouldRun != "open "+strings.Join(tc.want, " ") {
				t.Fatalf("dry run = %+v err=%v", dry, err)
			}
		})
	}
}

func TestLaunchRunsBehindQueuedInject(t *testing.T) {
	ft := &FakeTalon{TargetsList: []Target{{Name: "Notes"}, {Name: "Safari"}}}
	s := NewService(ft, 0, nil)
	t.Cleanup(s.Stop)
	release := make(chan struct{})
	order := make(chan string, 2)
	s.SetLauncher(launcherFunc(func(Target) error { order <- "launch"; return nil }))
	s.SetBeadCreator(&blockingBead{release: release, order: order})
	id := s.Submit(Submission{Device: "d", Text: "hi", Mode: ModeBead, Store: "inbox"})
	launched := make(chan error, 1)
	go func() { _, err := s.Launch(LaunchRequest{Device: "d", App: "Safari"}); launched <- err }()
	select {
	case <-launched:
		t.Fatal("launch ran while a queued submission was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-launched; err != nil {
		t.Fatal(err)
	}
	waitOutcome(t, s, id)
	if first, second := <-order, <-order; first != "bead" || second != "launch" {
		t.Fatalf("order = %s, %s; want bead then launch", first, second)
	}
}

type launcherFunc func(Target) error

func (f launcherFunc) Launch(t Target) error { return f(t) }

type blockingBead struct {
	release chan struct{}
	order   chan string
}

func (b *blockingBead) Resolve(store string) (string, error) { return store, nil }

func (b *blockingBead) Create(store, text string) (string, string, error) {
	<-b.release
	b.order <- "bead"
	return "inbox-1", store, nil
}
