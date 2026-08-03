// Proves the create->submit death-window recovery primitive this ticket's
// identity --submit/--park id auto-resolution depends on. Mirrors (a subset
// of) packages/cli/src/resolve-handoff.test.ts's stubStore approach: a fake
// `<store>` executable on PATH that dispatches on its first arg.
package resolvehandoff

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubStore installs a fake `store` executable on PATH for the duration of
// the test. listJSON/showJSON are the bodies for `list`/`show`; a zero
// status means "not stubbed" (exits 3, so the resolver's fallback chain
// proceeds naturally).
func stubStore(t *testing.T, store, listJSON, showJSON string) {
	t.Helper()
	dir := t.TempDir()
	listStatus, showStatus := 3, 3
	if listJSON != "" {
		listStatus = 0
	}
	if showJSON != "" {
		showStatus = 0
	}
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n  list) printf '%%s' %s; exit %d;;\n  show) printf '%%s' %s; exit %d;;\n  *) exit 3;;\nesac\n",
		shQuote(listJSON), listStatus, shQuote(showJSON), showStatus)
	path := filepath.Join(dir, store)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestResolvesNewestOpenHandoffViaList(t *testing.T) {
	stubStore(t, "handoff", `[{"id":"handoff-1bk","status":"open"}]`, "")
	if got := ResolveCurrentHandoff("handoff", "mayor"); got != "handoff-1bk" {
		t.Errorf("got %q, want handoff-1bk", got)
	}
}

func TestReturnsFirstRowOfMultiRowList(t *testing.T) {
	stubStore(t, "handoff", `[{"id":"handoff-new","status":"in_progress"},{"id":"handoff-old","status":"open"}]`, "")
	if got := ResolveCurrentHandoff("handoff", "mayor"); got != "handoff-new" {
		t.Errorf("got %q, want handoff-new", got)
	}
}

func TestAcceptsSingleObjectListResponse(t *testing.T) {
	stubStore(t, "handoff", `{"id":"handoff-xyz","status":"open"}`, "")
	if got := ResolveCurrentHandoff("handoff", "mayor"); got != "handoff-xyz" {
		t.Errorf("got %q, want handoff-xyz", got)
	}
}

func TestFallsBackToCurrentWhenListYieldsNothing(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\n  list) exit 1;;\n  show) printf '%s' '[{\"id\":\"handoff-cur\",\"status\":\"in_progress\"}]'; exit 0;;\n  *) exit 3;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "handoff"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got := ResolveCurrentHandoff("handoff", "mayor"); got != "handoff-cur" {
		t.Errorf("got %q, want handoff-cur", got)
	}
}

func TestDoesNotReturnClosedCurrentHandoff(t *testing.T) {
	stubStore(t, "handoff", `[]`, `[{"id":"handoff-done","status":"closed"}]`)
	if got := ResolveCurrentHandoff("handoff", "mayor"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestReturnsEmptyWhenNothingOpenAnywhere(t *testing.T) {
	stubStore(t, "handoff", `[]`, `[]`)
	if got := ResolveCurrentHandoff("handoff", "mayor"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestReturnsEmptyOnUnparseableOutput(t *testing.T) {
	stubStore(t, "handoff", `not json`, `also not json`)
	if got := ResolveCurrentHandoff("handoff", "mayor"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestHonorsNonDefaultStoreName(t *testing.T) {
	stubStore(t, "myhandoff", `[{"id":"myhandoff-7","status":"open"}]`, "")
	if got := ResolveCurrentHandoff("myhandoff", "mayor"); got != "myhandoff-7" {
		t.Errorf("got %q, want myhandoff-7", got)
	}
}

func TestMissingStoreBinaryResolvesToEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if got := ResolveCurrentHandoff("handoff", "mayor"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestAgentDefaultsToEnv(t *testing.T) {
	t.Setenv("PARLAY_AGENT_ID", "mayor")
	stubStore(t, "handoff", `[{"id":"handoff-env","status":"open"}]`, "")
	if got := ResolveCurrentHandoff("handoff", ""); got != "handoff-env" {
		t.Errorf("got %q, want handoff-env", got)
	}
}

func TestDetectUnsubmittedFiresWhenNotPinned(t *testing.T) {
	recent := "2099-01-01T00:00:00Z" // far future so it's never "inherited" by age
	stubStore(t, "handoff", fmt.Sprintf(`[{"id":"handoff-1bk","status":"open","created":%q}]`, recent), "")
	r, ok := DetectUnsubmittedHandoff("", "handoff", "mayor", nil)
	if !ok || r.ID != "handoff-1bk" {
		t.Errorf("got %+v ok=%v, want handoff-1bk", r, ok)
	}
}

func TestDetectUnsubmittedDoesNotFlagWhenAlreadyPinned(t *testing.T) {
	stubStore(t, "handoff", `[{"id":"handoff-1bk","status":"open"}]`, "")
	if _, ok := DetectUnsubmittedHandoff("handoff-1bk", "handoff", "mayor", nil); ok {
		t.Error("expected no unsubmitted result when already pinned")
	}
}

func TestDetectUnsubmittedFlagsDifferentHandoffEvenWithOlderPinned(t *testing.T) {
	stubStore(t, "handoff", `[{"id":"handoff-new","status":"open"}]`, "")
	r, ok := DetectUnsubmittedHandoff("handoff-old", "handoff", "mayor", nil)
	if !ok || r.ID != "handoff-new" {
		t.Errorf("got %+v ok=%v, want handoff-new", r, ok)
	}
}

func TestDetectUnsubmittedNoneWhenClean(t *testing.T) {
	stubStore(t, "handoff", `[]`, `[]`)
	if _, ok := DetectUnsubmittedHandoff("", "handoff", "mayor", nil); ok {
		t.Error("expected no unsubmitted result on clean state")
	}
}

func TestInheritedTrueWhenOlderThan24h(t *testing.T) {
	stale := "2000-01-01T00:00:00Z"
	stubStore(t, "handoff", fmt.Sprintf(`[{"id":"handoff-adz","status":"open","created":%q}]`, stale), "")
	r, ok := DetectUnsubmittedHandoff("", "handoff", "brain-dev", nil)
	if !ok || !r.Inherited {
		t.Errorf("got %+v ok=%v, want inherited=true", r, ok)
	}
}

func TestInheritedReadsCreatedAtField(t *testing.T) {
	stale := "2000-01-01T00:00:00Z"
	stubStore(t, "handoff", fmt.Sprintf(`[{"id":"handoff-real","status":"open","created_at":%q}]`, stale), "")
	r, ok := DetectUnsubmittedHandoff("", "handoff", "brain-dev", nil)
	if !ok || r.ID != "handoff-real" || !r.Inherited {
		t.Errorf("got %+v ok=%v, want handoff-real inherited=true", r, ok)
	}
}

func TestInheritedTrueWhenAgeUnknown(t *testing.T) {
	stubStore(t, "handoff", `[{"id":"handoff-unknown","status":"open"}]`, "")
	r, ok := DetectUnsubmittedHandoff("", "handoff", "brain-dev", nil)
	if !ok || !r.Inherited {
		t.Errorf("got %+v ok=%v, want inherited=true (unknown age defaults safe)", r, ok)
	}
}

func TestInheritedFalseWhenCreatedAfterSessionStart(t *testing.T) {
	sessionStart := int64(1_700_000_000_000)
	handoffCreated := "2023-11-15T00:00:00Z" // after sessionStart's epoch
	stubStore(t, "handoff", fmt.Sprintf(`[{"id":"handoff-current","status":"open","created":%q}]`, handoffCreated), "")
	r, ok := DetectUnsubmittedHandoff("", "handoff", "brain-dev", &sessionStart)
	if !ok || r.Inherited {
		t.Errorf("got %+v ok=%v, want inherited=false", r, ok)
	}
}
