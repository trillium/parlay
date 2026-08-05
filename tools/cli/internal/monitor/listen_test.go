// Unit tests for `parlay listen`. Mirrors packages/cli/src/listen.test.ts:
// register+announce hit a recording httptest server, and runMonitor is
// swapped for a recording fake so no real process is spawned and no test
// blocks forever in the poll loop.
package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

type recordedCall struct {
	path string
	body map[string]any
}

type listenHarness struct {
	mu    sync.Mutex
	calls []recordedCall
}

func startListenHarness(t *testing.T) *listenHarness {
	t.Helper()
	h := &listenHarness{}
	mux := http.NewServeMux()
	record := func(path string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			h.mu.Lock()
			h.calls = append(h.calls, recordedCall{path: path, body: body})
			h.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": "reply-1"})
		}
	}
	mux.HandleFunc("/api/chat/register-agent", record("/api/chat/register-agent"))
	mux.HandleFunc("/api/chat/reply", record("/api/chat/reply"))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", srv.URL)
	return h
}

// stubMonitor swaps runMonitor for a recording fake and restores the real
// one on cleanup.
func stubMonitor(t *testing.T) *[][]string {
	t.Helper()
	calls := &[][]string{}
	orig := runMonitor
	runMonitor = func(args []string) { *calls = append(*calls, args) }
	t.Cleanup(func() { runMonitor = orig })
	return calls
}

func TestCmdListenRequiresAgent(t *testing.T) {
	startListenHarness(t)
	monitorCalls := stubMonitor(t)
	trapExit(t)

	code, ok := testsupport.Capture(func() {
		CmdListen(nil)
	})
	if !ok {
		t.Fatal("expected Die when --agent is missing")
	}
	if code != config.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, config.ExitUsage)
	}
	if len(*monitorCalls) != 0 {
		t.Errorf("monitor should not be invoked, got %v", *monitorCalls)
	}
}

func TestCmdListenRegistersAnnouncesThenHandsOffToMonitor(t *testing.T) {
	h := startListenHarness(t)
	monitorCalls := stubMonitor(t)
	trapExit(t)

	CmdListen([]string{"--agent", "brain-dev"})

	if len(h.calls) != 2 {
		t.Fatalf("expected 2 HTTP calls, got %d: %+v", len(h.calls), h.calls)
	}
	if h.calls[0].path != "/api/chat/register-agent" {
		t.Errorf("call 0 path = %q, want register-agent", h.calls[0].path)
	}
	if h.calls[0].body["id"] != "brain-dev" || h.calls[0].body["name"] != "brain-dev" {
		t.Errorf("call 0 body = %+v, want id=name=brain-dev", h.calls[0].body)
	}
	if h.calls[1].path != "/api/chat/reply" {
		t.Errorf("call 1 path = %q, want reply", h.calls[1].path)
	}
	if h.calls[1].body["agent"] != "brain-dev" {
		t.Errorf("call 1 body agent = %v, want brain-dev", h.calls[1].body["agent"])
	}
	text, _ := h.calls[1].body["text"].(string)
	if !regexp.MustCompile(`listening`).MatchString(text) {
		t.Errorf("reply text = %q, want to mention 'listening'", text)
	}

	if len(*monitorCalls) != 1 {
		t.Fatalf("expected 1 monitor call, got %d", len(*monitorCalls))
	}
	want := []string{"--agent", "brain-dev"}
	got := (*monitorCalls)[0]
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("monitor args = %v, want %v", got, want)
	}
}

func TestCmdListenNameOverridesDefaultDisplayName(t *testing.T) {
	h := startListenHarness(t)
	stubMonitor(t)
	trapExit(t)

	CmdListen([]string{"--agent", "brain-dev", "--name", "Brain Dev"})

	if h.calls[0].body["id"] != "brain-dev" || h.calls[0].body["name"] != "Brain Dev" {
		t.Errorf("call 0 body = %+v, want id=brain-dev name=Brain Dev", h.calls[0].body)
	}
}

func TestCmdListenSameAgentIDAlwaysDerivesSameColor(t *testing.T) {
	h1 := startListenHarness(t)
	stubMonitor(t)
	trapExit(t)
	CmdListen([]string{"--agent", "brain-dev"})
	color1, _ := h1.calls[0].body["color"].(string)

	h2 := startListenHarness(t)
	stubMonitor(t)
	CmdListen([]string{"--agent", "brain-dev"})
	color2, _ := h2.calls[0].body["color"].(string)

	if color1 != color2 {
		t.Errorf("colors differ across runs: %q vs %q", color1, color2)
	}
	if !regexp.MustCompile(`^#[0-9a-f]{6}$`).MatchString(color1) {
		t.Errorf("color %q does not match #<6hex>", color1)
	}
}

func TestCmdListenCapsForwardsParsedJSON(t *testing.T) {
	h := startListenHarness(t)
	stubMonitor(t)
	trapExit(t)

	CmdListen([]string{"--agent", "brain-dev", "--caps", `{"tools":["bash"]}`})

	caps, ok := h.calls[0].body["caps"].(map[string]any)
	if !ok {
		t.Fatalf("caps not forwarded as an object: %+v", h.calls[0].body)
	}
	tools, ok := caps["tools"].([]any)
	if !ok || len(tools) != 1 || tools[0] != "bash" {
		t.Errorf("caps.tools = %+v, want [\"bash\"]", caps["tools"])
	}
}

func TestCmdListenInvalidCapsJSONDiesBeforeAnyNetworkCall(t *testing.T) {
	h := startListenHarness(t)
	stubMonitor(t)
	trapExit(t)

	code, ok := testsupport.Capture(func() {
		CmdListen([]string{"--agent", "brain-dev", "--caps", "{not json"})
	})
	if !ok {
		t.Fatal("expected Die on invalid --caps JSON")
	}
	if code != config.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, config.ExitUsage)
	}
	if len(h.calls) != 0 {
		t.Errorf("expected no HTTP calls before the caps validation, got %d", len(h.calls))
	}
}

func TestCmdListenLegacyPollIsForwardedToMonitor(t *testing.T) {
	startListenHarness(t)
	monitorCalls := stubMonitor(t)
	trapExit(t)

	CmdListen([]string{"--agent", "brain-dev", "--legacy-poll"})

	want := []string{"--agent", "brain-dev", "--legacy-poll"}
	got := (*monitorCalls)[0]
	if len(got) != len(want) {
		t.Fatalf("monitor args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("monitor args = %v, want %v", got, want)
		}
	}
}

func TestCmdListenNotifySafeIsForwardedToMonitor(t *testing.T) {
	startListenHarness(t)
	monitorCalls := stubMonitor(t)
	trapExit(t)

	// robots-w9ij: --notify-safe must reach the underlying monitor poll loop
	// so a claim-enrolled panel agent gets notification-truncation safety.
	CmdListen([]string{"--agent", "brain-dev", "--notify-safe"})

	want := []string{"--agent", "brain-dev", "--notify-safe"}
	got := (*monitorCalls)[0]
	if len(got) != len(want) {
		t.Fatalf("monitor args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("monitor args = %v, want %v", got, want)
		}
	}
}

func TestCmdListenForwardsBothLegacyPollAndNotifySafe(t *testing.T) {
	startListenHarness(t)
	monitorCalls := stubMonitor(t)
	trapExit(t)

	CmdListen([]string{"--agent", "brain-dev", "--legacy-poll", "--notify-safe"})

	// Order is deterministic: --legacy-poll appended before --notify-safe.
	want := []string{"--agent", "brain-dev", "--legacy-poll", "--notify-safe"}
	got := (*monitorCalls)[0]
	if len(got) != len(want) {
		t.Fatalf("monitor args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("monitor args = %v, want %v", got, want)
		}
	}
}

func TestCmdListenIsIdempotentAcrossReRuns(t *testing.T) {
	h := startListenHarness(t)
	monitorCalls := stubMonitor(t)
	trapExit(t)

	CmdListen([]string{"--agent", "brain-dev"})
	CmdListen([]string{"--agent", "brain-dev"})

	if len(h.calls) != 4 {
		t.Errorf("HTTP calls = %d, want 4 (2 per run, not accumulating state)", len(h.calls))
	}
	if len(*monitorCalls) != 2 {
		t.Errorf("monitor calls = %d, want 2", len(*monitorCalls))
	}
}

func TestCmdListenHelpDoesNotTouchNetworkOrMonitor(t *testing.T) {
	h := startListenHarness(t)
	monitorCalls := stubMonitor(t)
	trapExit(t)

	CmdListen([]string{"--help"})

	if len(h.calls) != 0 {
		t.Errorf("expected no HTTP calls, got %d", len(h.calls))
	}
	if len(*monitorCalls) != 0 {
		t.Errorf("expected no monitor calls, got %d", len(*monitorCalls))
	}
}

func TestCmdListenEmptyCapsFlagIsTreatedAsOmitted(t *testing.T) {
	// TS: `if (opts["--caps"])` is falsy for an empty string value, so
	// `--caps ""` behaves like omitting the flag entirely (no die, no key).
	h := startListenHarness(t)
	stubMonitor(t)
	trapExit(t)

	CmdListen([]string{"--agent", "brain-dev", "--caps", ""})

	if _, present := h.calls[0].body["caps"]; present {
		t.Errorf("caps should be omitted for an empty --caps value, body = %+v", h.calls[0].body)
	}
}
