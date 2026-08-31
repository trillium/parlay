package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/cityscaffold"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// setCityProvider rewrites the materialised scaffold's [session] provider —
// the test hook for exercising the capability gate against providers the
// authored city does not use. gcNudgeRun deliberately does not re-materialise
// (steering must never create or reconcile a city), so the edit sticks.
func setCityProvider(t *testing.T, provider string) string {
	t.Helper()
	scaffold, err := cityscaffold.Materialize()
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	path := filepath.Join(scaffold.Dir, "city.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), `provider = "subprocess"`, `provider = "`+provider+`"`, 1)
	if edited == string(data) && provider != "subprocess" {
		t.Fatalf("city.toml had no subprocess provider line to edit:\n%s", data)
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	return scaffold.Dir
}

func TestGCNudgeRefusesSubprocessProvider(t *testing.T) {
	testsupport.TempStateHome(t)
	setCityProvider(t, "subprocess")
	// A fake gc records any invocation; the refusal must happen BEFORE gc —
	// gc's own `session nudge` calls the subprocess provider's silent-nil
	// Nudge and would report the message delivered (R7).
	bin, rec := writeSpawnFakeGC(t, `{"ok":true}`, 0)
	t.Setenv("PARLAY_GC", bin)

	res, err := gcNudgeRun("agent-x", "pa-123", "kick")
	if err != nil {
		t.Fatalf("gcNudgeRun: %v", err)
	}
	if !res.Refused || res.OK {
		t.Errorf("subprocess provider must yield a refusal, got %+v", res)
	}
	if res.Provider != "subprocess" {
		t.Errorf("provider = %q", res.Provider)
	}
	for _, want := range []string{"no interactive injection", "silent nil no-op", "R7"} {
		if !strings.Contains(res.Reason, want) {
			t.Errorf("reason should contain %q, got: %s", want, res.Reason)
		}
	}
	if _, statErr := os.Stat(filepath.Join(rec, "argv")); !os.IsNotExist(statErr) {
		t.Error("gc was invoked despite the capability refusal — the gate must fire before any gc call")
	}
}

func TestGCNudgeRefusesUnknownProvider(t *testing.T) {
	testsupport.TempStateHome(t)
	setCityProvider(t, "acp")
	bin, rec := writeSpawnFakeGC(t, `{"ok":true}`, 0)
	t.Setenv("PARLAY_GC", bin)

	res, err := gcNudgeRun("agent-x", "pa-123", "kick")
	if err != nil {
		t.Fatalf("gcNudgeRun: %v", err)
	}
	if !res.Refused {
		t.Errorf("unknown provider must refuse (fail toward not steering), got %+v", res)
	}
	if !strings.Contains(res.Reason, `"acp"`) {
		t.Errorf("reason should name the provider, got: %s", res.Reason)
	}
	if _, statErr := os.Stat(filepath.Join(rec, "argv")); !os.IsNotExist(statErr) {
		t.Error("gc was invoked despite the refusal")
	}
}

func TestGCNudgeDelegatesForInjectionCapableProvider(t *testing.T) {
	testsupport.TempStateHome(t)
	cityDir := setCityProvider(t, "tmux")
	gcOut := `{"schema_version":"1","ok":true,"session_id":"pa-123"}`
	bin, rec := writeSpawnFakeGC(t, gcOut, 0)
	t.Setenv("PARLAY_GC", bin)

	res, err := gcNudgeRun("agent-x", "pa-123", "short kick")
	if err != nil {
		t.Fatalf("gcNudgeRun: %v", err)
	}
	if res.Refused || !res.OK {
		t.Errorf("tmux provider should delegate and confirm, got %+v", res)
	}
	if string(res.GCResult) != gcOut {
		t.Errorf("gc_result not passed through: %s", res.GCResult)
	}
	argv, readErr := os.ReadFile(filepath.Join(rec, "argv"))
	if readErr != nil {
		t.Fatalf("gc was never invoked: %v", readErr)
	}
	want := strings.Join([]string{"--city", cityDir, "session", "nudge", "pa-123", "short kick", "--json"}, "\n") + "\n"
	if string(argv) != want {
		t.Errorf("gc argv:\n%s\nwant:\n%s", argv, want)
	}
}

func TestGCNudgeReportsUnconfirmedDelivery(t *testing.T) {
	testsupport.TempStateHome(t)
	setCityProvider(t, "tmux")
	bin, _ := writeSpawnFakeGC(t, `{"schema_version":"1","ok":false}`, 1)
	t.Setenv("PARLAY_GC", bin)

	res, err := gcNudgeRun("agent-x", "pa-123", "kick")
	if err != nil {
		t.Fatalf("gcNudgeRun: %v", err)
	}
	if res.OK || res.Refused {
		t.Errorf("unconfirmed delivery is neither ok nor a refusal, got %+v", res)
	}
	if !strings.Contains(res.Reason, "did not confirm") {
		t.Errorf("reason = %s", res.Reason)
	}
}

func TestGCNudgeErrorsWithoutScaffold(t *testing.T) {
	testsupport.TempStateHome(t) // fresh state home: no city materialised

	_, err := gcNudgeRun("agent-x", "pa-123", "kick")
	if err == nil {
		t.Fatal("expected an error with no materialised scaffold")
	}
	if !strings.Contains(err.Error(), "city-scaffold") {
		t.Errorf("error should point at the scaffold verb, got: %v", err)
	}
}

func TestCitySessionProviderReadsAuthoredValue(t *testing.T) {
	testsupport.TempStateHome(t)
	scaffold, err := cityscaffold.Materialize()
	if err != nil {
		t.Fatal(err)
	}
	provider, err := citySessionProvider(scaffold.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if provider != "subprocess" {
		t.Errorf("authored provider = %q, want subprocess", provider)
	}
}
