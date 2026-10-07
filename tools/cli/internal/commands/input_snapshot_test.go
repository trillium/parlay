// The wiring proof for the stage-latency block: the derivation has its own
// unit tests next door, so this file exists to catch the one failure a unit
// test structurally cannot see — a correct rule that the verb never calls.
package commands

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestInputSnapshotCarriesTheStageLatencies drives the real verb against a
// fake server and asserts the operator's snapshot shows which hop was slow,
// not just how long one input took.
func TestInputSnapshotCarriesTheStageLatencies(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := latencyHops("m1", base,
		[]string{"queued", "delivered"},
		[]time.Duration{0, 25 * time.Second})
	withServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/input-events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(inputPage{
			Events: events,
			Stats:  inputStats{Retained: 2, Written: 2, NewestSeq: 2},
		})
	}))
	out := captureStdout(t, func() { Input(nil) })
	if !strings.Contains(out, "LATENCY BY STAGE") {
		t.Fatalf("the snapshot printed no stage latencies:\n%s", out)
	}
	if !strings.Contains(out, "queued→delivered") {
		t.Errorf("the snapshot did not name the slow stage:\n%s", out)
	}
	if !strings.Contains(out, "25.0s") {
		t.Errorf("the snapshot did not print the measured wait:\n%s", out)
	}
}

// The JSON half is additive: a script that already reads events/stats keeps
// reading them where they were, and the latencies arrive beside them.
func TestInputSnapshotJSONKeepsThePageAndAddsLatencies(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := latencyHops("m1", base,
		[]string{"queued", "delivered"},
		[]time.Duration{0, 3 * time.Second})
	withServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/input-events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(inputPage{Events: events, Stats: inputStats{Retained: 2}})
	}))
	out := captureStdout(t, func() { Input([]string{"--json"}) })
	var decoded struct {
		Events       []inputEvent       `json:"events"`
		Stats        inputStats         `json:"stats"`
		StageLatency []stageLatencyWire `json:"stageLatency"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("--json is not decodable: %v\n%s", err, out)
	}
	if len(decoded.Events) != 2 || decoded.Stats.Retained != 2 {
		t.Errorf("events/stats moved:\n%s", out)
	}
	if len(decoded.StageLatency) == 0 {
		t.Errorf("--json dropped the stage latencies:\n%s", out)
	}
}
