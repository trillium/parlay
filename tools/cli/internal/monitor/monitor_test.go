package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

func trapExit(t *testing.T) {
	t.Helper()
	orig := httpc.Exit
	httpc.Exit = testsupport.RecordingExit()
	t.Cleanup(func() { httpc.Exit = orig })
}

func TestCmdMonitorRequiresAgentUnlessLegacyPoll(t *testing.T) {
	trapExit(t)
	code, ok := testsupport.Capture(func() {
		CmdMonitor(nil)
	})
	if !ok {
		t.Fatal("expected Die when neither --agent nor --legacy-poll is given")
	}
	if code != config.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, config.ExitUsage)
	}
}

func TestCmdMonitorHelpDoesNotDie(t *testing.T) {
	trapExit(t)
	_, ok := testsupport.Capture(func() {
		CmdMonitor([]string{"--help"})
	})
	if ok {
		t.Fatal("--help should print usage and return, not die")
	}
}

func TestScriptPathPrefersPATH(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "parlay-monitor.sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := scriptPath()
	if err != nil {
		t.Fatalf("scriptPath() error = %v", err)
	}
	resolvedGot, _ := filepath.EvalSymlinks(got)
	resolvedStub, _ := filepath.EvalSymlinks(stub)
	if resolvedGot != resolvedStub {
		t.Errorf("scriptPath() = %q, want %q (the PATH stub)", got, stub)
	}
}

func TestScriptPathFallsBackToRepoRelativeLocation(t *testing.T) {
	// No stub on PATH here — this should resolve tools/monitor/parlay-monitor.sh
	// relative to this source file's location in the actual checkout.
	got, err := scriptPath()
	if err != nil {
		t.Fatalf("scriptPath() error = %v", err)
	}
	if filepath.Base(got) != "parlay-monitor.sh" {
		t.Errorf("scriptPath() = %q, want a path ending in parlay-monitor.sh", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("resolved script path does not exist: %v", err)
	}
	_, thisFile, _, _ := runtime.Caller(0)
	wantSuffix := filepath.Join("tools", "monitor", "parlay-monitor.sh")
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("scriptPath() = %q, want suffix %q (this test file: %s)", got, wantSuffix, thisFile)
	}
}

func TestNotifyBudgetFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{"unset falls back to 400", "", 400},
		{"non-numeric falls back to 400", "not-a-number", 400},
		{"zero falls back to 400 (JS 0 || 400 semantics)", "0", 400},
		{"valid value is used", "250", 250},
		{"negative value is used (only 0/NaN fall back)", "-5", -5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == "" {
				t.Setenv("PARLAY_NOTIFY_BUDGET", "")
				os.Unsetenv("PARLAY_NOTIFY_BUDGET")
			} else {
				t.Setenv("PARLAY_NOTIFY_BUDGET", tc.env)
			}
			if got := notifyBudgetFromEnv(); got != tc.want {
				t.Errorf("notifyBudgetFromEnv() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPollOnceNetworkErrorSleeps3s(t *testing.T) {
	lastID := ""
	var out strings.Builder
	got := pollOnce("http://127.0.0.1:1", "", &lastID, false, 400, &out)
	if got.sleep != 3*time.Second {
		t.Errorf("sleep = %v, want 3s", got.sleep)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output on network error, got %q", out.String())
	}
}

func TestPollOnceNon2xxSleeps2s(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	got := pollOnce(srv.URL, "", &lastID, false, 400, &out)
	if got.sleep != 2*time.Second {
		t.Errorf("sleep = %v, want 2s", got.sleep)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output on non-2xx, got %q", out.String())
	}
}

func TestPollOnceInvalidJSONSleeps3s(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	got := pollOnce(srv.URL, "", &lastID, false, 400, &out)
	if got.sleep != 3*time.Second {
		t.Errorf("sleep = %v, want 3s", got.sleep)
	}
}

func TestPollOnceTimeoutMessageIsQuietAndImmediate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"timeout": true})
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	got := pollOnce(srv.URL, "", &lastID, false, 400, &out)
	if got.sleep != 0 {
		t.Errorf("sleep = %v, want 0 (no delay on bare timeout)", got.sleep)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output on timeout, got %q", out.String())
	}
}

func TestPollOnceEmitsChatMsgAndAdvancesLastID(t *testing.T) {
	var gotAfter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAfter = r.URL.Query().Get("after")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "msg-1", "role": "user", "text": "hello", "from": "captain",
		})
	}))
	defer srv.Close()

	lastID := "msg-0"
	var out strings.Builder
	got := pollOnce(srv.URL, "", &lastID, false, 400, &out)
	if got.sleep != 0 {
		t.Errorf("sleep = %v, want 0", got.sleep)
	}
	if gotAfter != "msg-0" {
		t.Errorf("server saw after=%q, want msg-0", gotAfter)
	}
	if lastID != "msg-1" {
		t.Errorf("lastID = %q, want msg-1 (advanced)", lastID)
	}
	want := "CHAT_MSG|msg-1|user|hello|from:captain\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

func TestPollOnceSkipsIncompleteMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-1"}) // no role/text
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	got := pollOnce(srv.URL, "", &lastID, false, 400, &out)
	if got.sleep != 0 {
		t.Errorf("sleep = %v, want 0", got.sleep)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output for an incomplete message, got %q", out.String())
	}
	if lastID != "" {
		t.Errorf("lastID should not advance on an incomplete message, got %q", lastID)
	}
}

func TestPollOnceEmptyTextStillEmits(t *testing.T) {
	// TS: `msg.text != null` — empty string is NOT null, so it still emits.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "user", "text": ""})
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	pollOnce(srv.URL, "", &lastID, false, 400, &out)
	want := "CHAT_MSG|msg-1|user|\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

func TestPollOnceNotifySafeTruncatesLongLines(t *testing.T) {
	longText := strings.Repeat("x", 500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "user", "text": longText})
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	pollOnce(srv.URL, "", &lastID, true, 100, &out)
	got := out.String()
	if strings.Contains(got, longText) {
		t.Errorf("expected truncation, but full text present: %q", got)
	}
	if !strings.Contains(got, "chars truncated for notification") {
		t.Errorf("expected truncation marker, got %q", got)
	}
	if !strings.HasPrefix(got, "CHAT_MSG|msg-1|user|"+strings.Repeat("x", 100-len("CHAT_MSG|msg-1|user|"))) {
		t.Errorf("truncated line does not start with the expected budget-length prefix: %q", got)
	}
}

func TestPollOnceNotifySafeLeavesShortLinesAlone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "user", "text": "short"})
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	pollOnce(srv.URL, "", &lastID, true, 400, &out)
	want := "CHAT_MSG|msg-1|user|short\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

func TestPollOnceStopsOn410Gone(t *testing.T) {
	// robots-ycfa: a tombstoned channel answers 410, and retrying re-creates
	// it and polls forever. The Go port used to fold 410 into the generic
	// non-2xx 2s retry, which kept the leak alive on the path bin/parlay
	// actually execs (robots-jkwc).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		w.Write([]byte(`{"gone":true,"error":"channel was unregistered; stop polling"}`))
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	got := pollOnce(srv.URL, "&channel=ghost-z1", &lastID, false, 400, &out)
	if !got.stop {
		t.Error("410 Gone must be terminal for the poll loop")
	}
	if got.sleep != 0 {
		t.Errorf("a terminal answer must not also ask for a retry sleep, got %v", got.sleep)
	}
	if out.Len() != 0 {
		t.Errorf("expected no CHAT_MSG output on 410, got %q", out.String())
	}
}

func TestPollOnceKeepsRetryingOnAServerError(t *testing.T) {
	// 410 is the ONLY terminal status — a 500 is a transient server problem
	// and must never retire a live agent's monitor.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	lastID := ""
	var out strings.Builder
	got := pollOnce(srv.URL, "", &lastID, false, 400, &out)
	if got.stop {
		t.Error("a 500 must not stop the poll loop")
	}
	if got.sleep != 2*time.Second {
		t.Errorf("sleep = %v, want 2s", got.sleep)
	}
}
