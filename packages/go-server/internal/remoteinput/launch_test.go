package remoteinput

import (
	"errors"
	"testing"
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
	if len(fl.Calls) != 1 || fl.Calls[0] != "Notes" || len(ft.FocusAppCalls) != 1 {
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
