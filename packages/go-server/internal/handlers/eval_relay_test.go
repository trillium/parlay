package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// resetStreamTable clears the package-level stream table so tests that fill it
// do not leak entries into each other.
func resetStreamTable(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		streamDeviceMapMu.Lock()
		streamDeviceMap = make(map[string]string)
		streamOrder = nil
		streamDeviceMapMu.Unlock()
	})
	streamDeviceMapMu.Lock()
	streamDeviceMap = make(map[string]string)
	streamOrder = nil
	streamDeviceMapMu.Unlock()
}

// fakeEngine stands up an httptest server posing as the eval engine and points
// the relay at it for the duration of the test.
func fakeEngine(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv("PARLAY_EVAL_ENGINE_URL", srv.URL)
}

func postEval(t *testing.T, hub *Hub, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/eval", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleEval(hub)(rec, req)
	return rec
}

// A non-200 from the engine used to be returned as (nil, nil) — success with an
// empty body — because the status check returned the already-nil `err`. The
// caller then failed to unmarshal nil and blamed the engine for an "invalid
// response", so every real engine error (a 500, a 404 from a misconfigured
// PARLAY_EVAL_ENGINE_URL) surfaced as the same wrong message.
func TestRelaySurfacesEngineStatusRatherThanBlankResponse(t *testing.T) {
	resetStreamTable(t)
	fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "engine exploded", http.StatusInternalServerError)
	})

	rec := postEval(t, newHub(newBroker()), `{"device":"d1","text":"hi"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "500") {
		t.Errorf("error %q does not name the engine's status; a 500 is indistinguishable from a malformed body", body)
	}
	if strings.Contains(body, "invalid engine response") {
		t.Errorf("a non-200 was reported as a malformed body: %q", body)
	}
}

// A body larger than the cap must be refused rather than read into memory.
func TestRelayRejectsOversizedEngineResponse(t *testing.T) {
	resetStreamTable(t)
	fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Well over maxEngineResponse, streamed rather than allocated at once.
		chunk := strings.Repeat("a", 64*1024)
		w.Write([]byte(`{"pad":"`))
		for written := 0; written < maxEngineResponse+len(chunk); written += len(chunk) {
			w.Write([]byte(chunk))
		}
		w.Write([]byte(`"}`))
	})

	rec := postEval(t, newHub(newBroker()), `{"device":"d1","text":"hi"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "exceeds") {
		t.Errorf("oversized body not reported as such: %q", rec.Body.String())
	}
}

func TestRelayReturnsEngineEnvelope(t *testing.T) {
	resetStreamTable(t)
	fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("engine got undecodable request: %v", err)
		}
		// The relay fills in a default streamId and reason when the client omits them.
		if got["streamId"] != "eval-d1-main" {
			t.Errorf("engine saw streamId %v, want the generated eval-d1-main", got["streamId"])
		}
		if got["reason"] != "input" {
			t.Errorf("engine saw reason %v, want the default \"input\"", got["reason"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"v":1,"streamId":"eval-d1-main","seq":7,"baseVersion":3,"actions":[{"kind":"submitNow"}],"engineEvalNs":1234}`))
	})

	rec := postEval(t, newHub(newBroker()), `{"device":"d1","text":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["seq"] != float64(7) || resp["baseVersion"] != float64(3) {
		t.Errorf("envelope not passed through: %+v", resp)
	}
	if actions, ok := resp["actions"].([]any); !ok || len(actions) != 1 {
		t.Errorf("actions not passed through: %+v", resp["actions"])
	}
}

// The stream table is keyed by caller-supplied ids on an API with no
// authentication, so it must not grow without bound.
func TestStreamTableIsBounded(t *testing.T) {
	resetStreamTable(t)

	for i := 0; i < maxTrackedStreams+500; i++ {
		rememberStream("stream-"+itoa(i), "device-"+itoa(i))
	}

	streamDeviceMapMu.RLock()
	size, order := len(streamDeviceMap), len(streamOrder)
	streamDeviceMapMu.RUnlock()

	if size > maxTrackedStreams {
		t.Errorf("stream table holds %d entries, over the %d cap", size, maxTrackedStreams)
	}
	if order != size {
		t.Errorf("eviction list (%d) drifted from the map (%d) — one leaks without the other", order, size)
	}
	// Oldest-first eviction: the earliest ids are gone, the newest survive.
	if _, ok := deviceForStream("stream-0"); ok {
		t.Errorf("stream-0 survived past the cap; eviction is not oldest-first")
	}
	if _, ok := deviceForStream("stream-" + itoa(maxTrackedStreams+499)); !ok {
		t.Errorf("the most recent stream was evicted")
	}
}

// Re-declaring an existing stream must re-point it, not append a second
// eviction-list entry — otherwise a client that re-evals on the same stream id
// (the normal case, since the default id is stable per device) pushes the list
// past the map and evicts live entries.
func TestRememberStreamDoesNotDoubleCountRepeats(t *testing.T) {
	resetStreamTable(t)

	for i := 0; i < 50; i++ {
		rememberStream("same-stream", "device-a")
	}

	streamDeviceMapMu.RLock()
	size, order := len(streamDeviceMap), len(streamOrder)
	streamDeviceMapMu.RUnlock()

	if size != 1 || order != 1 {
		t.Errorf("50 repeats of one stream produced map=%d order=%d, want 1 and 1", size, order)
	}

	rememberStream("same-stream", "device-b")
	if got, _ := deviceForStream("same-stream"); got != "device-b" {
		t.Errorf("re-declare did not re-point the device: got %q", got)
	}
}

// itoa avoids pulling strconv in just for test labels.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
