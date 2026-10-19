package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// rodeOKReading is a producer frame in exactly the shape rode-battery writes
// to its state/latest.json (one connected mic at 72%, one disconnected with a
// null pct).
const rodeOKReading = `{"status":"ok","ts":"2026-10-19T04:17:15.511Z","channels":[{"mic":"tx1","label":"Wireless GO II TX1","connected":true,"pct":72,"color":"green","confidence":0.9},{"mic":"tx2","label":"Wireless GO II TX2","connected":false,"pct":null,"color":"unknown","confidence":0.9}]}`

func getRode(t *testing.T, rode *rodeStore) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handleRode(rode)(rec, httptest.NewRequest(http.MethodGet, "/api/rode", nil))
	return rec
}

func TestRodeStatusIngressFansOutAndServesTheLatestReading(t *testing.T) {
	hub := newHub(newBroker())
	t.Cleanup(hub.Stop)
	rode := newRodeStore()
	a, cancelA := hub.subscribe("")
	defer cancelA()
	b, cancelB := hub.subscribe("")
	defer cancelB()

	rec := postIngressRode(t, hub, rode, `{"event":"rode_status","data":`+rodeOKReading+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp eventIngressResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK || resp.Event != "rode_status" {
		t.Fatalf("response = %+v, want ok + rode_status", resp)
	}

	// Every SSE client sees the producer's bytes untouched — the capture
	// for GET must not reshape what goes on the wire.
	for i, sub := range []<-chan sseEvent{a, b} {
		ev := awaitEvent(t, sub, "rode_status", time.Second)
		got, err := json.Marshal(ev.data)
		if err != nil {
			t.Fatalf("client %d: marshal payload: %v", i, err)
		}
		if string(got) != rodeOKReading {
			t.Errorf("client %d payload = %s, want %s", i, got, rodeOKReading)
		}
	}

	// GET /api/rode serves the same reading in the producer's shape: the
	// null pct survives, the optional detail is absent, nothing is wrapped
	// around it.
	get := getRode(t, rode)
	if get.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body: %s", get.Code, get.Body.String())
	}
	if ct := get.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("GET Content-Type = %q, want application/json", ct)
	}
	var want, got any
	if err := json.Unmarshal([]byte(rodeOKReading), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET body is not JSON: %v: %s", err, get.Body.String())
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("GET body = %s, want %s", gotJSON, wantJSON)
	}
}

func TestRodeStatusLatestReadingReplacesThePreviousOne(t *testing.T) {
	hub := newHub(newBroker())
	t.Cleanup(hub.Stop)
	rode := newRodeStore()

	if rec := postIngressRode(t, hub, rode, `{"event":"rode_status","data":`+rodeOKReading+`}`); rec.Code != http.StatusOK {
		t.Fatalf("first post: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	// A non-ok capture carries no channels and a detail; the previous
	// channels must not leak through into it.
	second := `{"status":"app-not-running","ts":"2026-10-19T04:18:00.000Z","detail":"RODE Connect process absent"}`
	if rec := postIngressRode(t, hub, rode, `{"event":"rode_status","data":`+second+`}`); rec.Code != http.StatusOK {
		t.Fatalf("second post: status = %d; body: %s", rec.Code, rec.Body.String())
	}

	get := getRode(t, rode)
	if get.Code != http.StatusOK {
		t.Fatalf("GET status = %d; body: %s", get.Code, get.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "app-not-running" || got["ts"] != "2026-10-19T04:18:00.000Z" || got["detail"] != "RODE Connect process absent" {
		t.Errorf("GET body = %s, want the second reading", get.Body.String())
	}
	if _, leaked := got["channels"]; leaked {
		t.Errorf("GET body = %s: channels from the previous reading leaked into a non-ok capture", get.Body.String())
	}
}

func TestRodeGetBeforeAnyReadingIs404(t *testing.T) {
	rec := getRode(t, newRodeStore())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Fatalf("body = %s, want {\"error\": ...}", rec.Body.String())
	}
}

func TestRodeGetRejectsOtherMethods(t *testing.T) {
	rode := newRodeStore()
	rode.set(RodeStatus{Status: "ok", TS: "2026-10-19T04:17:15.511Z"})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		handleRode(rode)(rec, httptest.NewRequest(method, "/api/rode", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
			t.Errorf("%s: Allow = %q, want GET", method, allow)
		}
	}
}

// A rode_status frame that is not a reading is the producer's bug: refused
// with a 400, not stored, and — unlike every other ingress event — not
// broadcast either, because GET /api/rode would then contradict the wire.
func TestRodeStatusIngressRefusesANonReading(t *testing.T) {
	hub := newHub(newBroker())
	t.Cleanup(hub.Stop)
	rode := newRodeStore()
	sub, cancel := hub.subscribe("")
	defer cancel()

	for name, body := range map[string]string{
		"no data":          `{"event":"rode_status"}`,
		"not an object":    `{"event":"rode_status","data":"ok"}`,
		"missing status":   `{"event":"rode_status","data":{"ts":"2026-10-19T04:17:15.511Z"}}`,
		"missing ts":       `{"event":"rode_status","data":{"status":"ok"}}`,
		"channels not arr": `{"event":"rode_status","data":{"status":"ok","ts":"t","channels":{}}}`,
		"pct not a number": `{"event":"rode_status","data":{"status":"ok","ts":"t","channels":[{"mic":"tx1","pct":"full"}]}}`,
	} {
		rec := postIngressRode(t, hub, rode, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body: %s", name, rec.Code, rec.Body.String())
		}
	}
	if _, seen := rode.get(); seen {
		t.Error("a refused frame was stored")
	}
	select {
	case ev := <-sub:
		t.Fatalf("a refused rode_status broadcast %q", ev.name)
	case <-time.After(50 * time.Millisecond):
	}
}

// The reading reaches GET /api/rode through the real mux registration, and
// the route answers at the path the producer's consumers will poll.
func TestRodeRouteIsRegisteredByRegister(t *testing.T) {
	st := newTestStore(t)
	mux := http.NewServeMux()
	hub := Register(mux, st)
	t.Cleanup(hub.Stop)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/rode", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET before a reading: status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}

	post := httptest.NewRequest(http.MethodPost, "/api/chat/events", strings.NewReader(`{"event":"rode_status","data":`+rodeOKReading+`}`))
	post.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, post)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST: status = %d; body: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/rode", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET after a reading: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var got RodeStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "ok" || len(got.Channels) != 2 || got.Channels[0].Pct == nil || *got.Channels[0].Pct != 72 || got.Channels[1].Pct != nil {
		t.Errorf("GET body = %s, want the posted reading", rec.Body.String())
	}
}
