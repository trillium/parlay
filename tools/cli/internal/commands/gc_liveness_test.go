package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

func fastLivenessPoll(t *testing.T) {
	t.Helper()
	old := gcLivenessPollInterval
	gcLivenessPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { gcLivenessPollInterval = old })
}

// subscribersServer serves GET /api/chat/subscribers in the REAL response
// shape both servers emit (an object, never a bare array — the original
// helper here served an array and hid that the production parse could never
// match). The given channels appear as open poll waiters; each also gets a
// listening presence row, and a decoy "registered-only" agent proves that
// registration alone is not treated as liveness.
func subscribersServer(t *testing.T, channels ...string) *httptest.Server {
	t.Helper()
	pollChannels := make([]map[string]any, 0, len(channels))
	presence := make([]map[string]any, 0, len(channels)+1)
	for _, c := range channels {
		pollChannels = append(pollChannels, map[string]any{"channel": c})
		presence = append(presence, map[string]any{"channel": c, "listening": true, "status": "listening"})
	}
	// The pre-registered-but-never-active decoy: what bin/parlay-spawn's
	// register-agent POST alone produces (Bun server shape).
	presence = append(presence, map[string]any{
		"channel": "registered-only-decoy", "listening": false, "status": "offline", "lastSeen": nil,
	})
	body := map[string]any{
		"parlay":     map[string]any{"clients": 0},
		"poll":       map[string]any{"count": len(pollChannels), "channels": pollChannels},
		"registered": map[string]any{"count": len(channels) + 1},
		"presence":   presence,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/subscribers" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGCLivenessConfirmsFromSubscribers(t *testing.T) {
	testsupport.TempStateHome(t)
	fastLivenessPoll(t)
	srv := subscribersServer(t, "other-agent", "agent-x")

	res := gcLivenessRun("agent-x", "pa-123", srv.URL, 5*time.Second)
	if !res.OK || !res.Confirmed {
		t.Fatalf("expected confirmation, got %+v", res)
	}
	if res.Via != "subscribers" {
		t.Errorf("via = %q", res.Via)
	}
	if res.Steer != nil {
		t.Errorf("confirmed run must not steer, got steer %+v", res.Steer)
	}
}

// TestGCLivenessTimeoutOnSubprocessRefusesSteering is THE unit-6 contract
// test: when the startup turn is never observed on the subprocess city, the
// watchdog reports a typed refusal and gc is never invoked — so no charter
// (and no kick) can be re-prompted into a session whose provider cannot
// inject. Assertion is on emitted output (the envelope + the fake gc's argv
// file), never elapsed time; the timeout below is a poll bound.
func TestGCLivenessTimeoutOnSubprocessRefusesSteering(t *testing.T) {
	testsupport.TempStateHome(t)
	fastLivenessPoll(t)
	setCityProvider(t, "subprocess")
	bin, rec := writeSpawnFakeGC(t, `{"ok":true}`, 0)
	t.Setenv("PARLAY_GC", bin)
	srv := subscribersServer(t) // empty: agent never enrolls

	res := gcLivenessRun("agent-x", "pa-123", srv.URL, 50*time.Millisecond)
	if res.OK || res.Confirmed {
		t.Fatalf("expected unconfirmed report, got %+v", res)
	}
	if res.Steer == nil || !res.Steer.Refused {
		t.Fatalf("steering must be a typed refusal on the subprocess provider, got %+v", res.Steer)
	}
	if _, statErr := os.Stat(filepath.Join(rec, "argv")); !os.IsNotExist(statErr) {
		t.Error("gc was invoked on the report path — the watchdog must be structurally unable to prompt a subprocess session")
	}
}

func TestGCLivenessTimeoutOnTmuxDeliversFixedKick(t *testing.T) {
	testsupport.TempStateHome(t)
	fastLivenessPoll(t)
	cityDir := setCityProvider(t, "tmux")
	bin, rec := writeSpawnFakeGC(t, `{"schema_version":"1","ok":true}`, 0)
	t.Setenv("PARLAY_GC", bin)
	srv := subscribersServer(t)

	res := gcLivenessRun("agent-x", "pa-123", srv.URL, 50*time.Millisecond)
	if res.Confirmed {
		t.Fatalf("expected report path, got %+v", res)
	}
	if res.Steer == nil || res.Steer.Refused || !res.Steer.OK {
		t.Fatalf("tmux steer should deliver, got %+v", res.Steer)
	}
	if !res.OK {
		t.Errorf("delivered kick should surface as ok, got %+v", res)
	}
	argv, err := os.ReadFile(filepath.Join(rec, "argv"))
	if err != nil {
		t.Fatalf("gc never invoked: %v", err)
	}
	// Exact argv: the steered message is the fixed kick — never the charter —
	// and nothing else message-shaped may ride along.
	want := strings.Join([]string{"--city", cityDir, "session", "nudge", "pa-123", gcLivenessKick, "--json"}, "\n") + "\n"
	if string(argv) != want {
		t.Errorf("gc argv:\n%s\nwant:\n%s", argv, want)
	}
}

// TestGCLivenessRegistrationAloneDoesNotConfirm pins the false-positive
// boundary: bin/parlay-spawn's pre-launch register-agent POST makes the
// channel appear under `registered` and as an offline presence row before
// the agent has done anything. That must never read as liveness.
func TestGCLivenessRegistrationAloneDoesNotConfirm(t *testing.T) {
	testsupport.TempStateHome(t)
	fastLivenessPoll(t)
	setCityProvider(t, "subprocess")
	bin, _ := writeSpawnFakeGC(t, `{"ok":true}`, 0)
	t.Setenv("PARLAY_GC", bin)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"poll":       map[string]any{"count": 0, "channels": []map[string]any{}},
			"registered": map[string]any{"count": 1, "agents": []map[string]any{{"id": "agent-x", "name": "Agent X"}}},
			"presence": []map[string]any{
				{"channel": "agent-x", "listening": false, "status": "offline", "lastSeen": nil},
			},
		})
	}))
	t.Cleanup(srv.Close)

	res := gcLivenessRun("agent-x", "pa-123", srv.URL, 50*time.Millisecond)
	if res.Confirmed {
		t.Fatalf("registration alone confirmed liveness — the registered/offline rows are the spawner's writes, not the agent's: %+v", res)
	}
}

func TestGCLivenessConfirmSkipsSlowPollTail(t *testing.T) {
	// The agent enrolls between polls: liveness must confirm on a later poll,
	// not give up after the first miss.
	testsupport.TempStateHome(t)
	fastLivenessPoll(t)
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) >= 3 {
			json.NewEncoder(w).Encode(map[string]any{
				"poll": map[string]any{"count": 1, "channels": []map[string]any{{"channel": "agent-x"}}},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"poll": map[string]any{"count": 0, "channels": []map[string]any{}},
		})
	}))
	t.Cleanup(srv.Close)

	res := gcLivenessRun("agent-x", "pa-123", srv.URL, 5*time.Second)
	if !res.Confirmed {
		t.Fatalf("expected eventual confirmation, got %+v", res)
	}
	if calls.Load() < 3 {
		t.Errorf("confirmed after %d polls; the poll loop should have retried", calls.Load())
	}
}

func TestGCLivenessResultEnvelopeShape(t *testing.T) {
	steer := gcNudgeResult{AgentID: "a", SessionID: "s", Provider: "subprocess", Refused: true, Reason: "r"}
	res := gcLivenessResult{AgentID: "a", SessionID: "s", Steer: &steer}
	out, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ok", "agent_id", "session_id", "confirmed", "steer"} {
		if _, present := m[key]; !present {
			t.Errorf("envelope missing %q: %s", key, out)
		}
	}
	steerMap, _ := m["steer"].(map[string]any)
	for _, key := range []string{"ok", "agent_id", "session_id", "provider", "refused", "reason"} {
		if _, present := steerMap[key]; !present {
			t.Errorf("steer envelope missing %q: %s", key, out)
		}
	}
}
