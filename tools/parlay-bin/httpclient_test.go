package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// docs/scope-go-spawn.md Finding F2 / #236: registerAgent must stamp
// launchedBy/startedAt, the exact literal packages/server/src/types.ts
// documents ("parlay-spawn" | "parlay-claim") and
// packages/server/src/prune/idle-reap.ts's shouldIdleReap keys its
// "parlay"-prefix reap-eligibility test on. Without these fields a
// Go-spawned agent is silently exempt from idle reaping — treated the same
// as a firstmate-spawned one.
func TestRegisterAgentSendsLaunchedByAndStartedAt(t *testing.T) {
	var got map[string]any
	before := time.Now().UTC()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decoding register-agent body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	if err := registerAgent(srv.URL, "mc-x", "MC X", "#c084fc"); err != nil {
		t.Fatalf("registerAgent: %v", err)
	}
	after := time.Now().UTC()

	if got["launchedBy"] != "parlay-spawn" {
		t.Errorf("launchedBy = %v, want \"parlay-spawn\"", got["launchedBy"])
	}
	startedAt, _ := got["startedAt"].(string)
	ts, err := time.Parse("2006-01-02T15:04:05Z", startedAt)
	if err != nil {
		t.Fatalf("startedAt %q is not RFC3339 UTC (%q format): %v", startedAt, "2006-01-02T15:04:05Z", err)
	}
	if ts.Before(before.Add(-time.Second)) || ts.After(after.Add(time.Second)) {
		t.Errorf("startedAt %v is outside the [%v, %v] call window", ts, before, after)
	}
}
