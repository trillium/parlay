package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"parlay/go-server/internal/store"
)

// The read route implements every axis the spec names, and the payload carries
// the vocabulary a renderer needs to build the controls for them.
func TestActionLogRouteFiltersAndVocabulary(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC()
	st.ActionLog.Append(store.ActionRecord{Source: "test-site", Device: "d1", InputAction: "clear", OutputActions: []string{"clear"}, Outcome: store.OutcomeDelivered})
	st.ActionLog.Append(store.ActionRecord{Source: "panel", Device: "d2", InputAction: "submit", OutputActions: []string{"armTimer"}, Outcome: store.OutcomeQueued, Reason: "submit-armed"})
	st.ActionLog.Append(store.ActionRecord{Source: "panel", Device: "d2", InputAction: "submit", OutputActions: []string{"submitNow"}, Outcome: store.OutcomeRefused, Reason: "off-action"})
	_ = now
	h := handleActionLog(st)
	get := func(query string) map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/chat/action-log?"+query, nil)
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET ?%s: status %d body %s", query, rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}

	if got := get(""); got["total"] != float64(3) {
		t.Errorf("unfiltered total = %v, want 3", got["total"])
	}

	cases := map[string]float64{
		"inputAction=clear":                  1,
		"outputAction=armTimer":              1,
		"outcome=refused":                    1,
		"outcome=refused&inputAction=submit": 1,
		"source=test-site":                   1,
		"device=d2":                          2,
		"reason=submit-armed":                1,
		"outcome=delivered&device=d2":        0,
	}
	for query, want := range cases {
		if got := get(query); got["total"] != want {
			t.Errorf("?%s total = %v, want %v", query, got["total"], want)
		}
	}

	// The four-value outcome vocabulary is served, not hard-coded per renderer.
	full := get("")
	vocab, _ := full["outcomeVocabulary"].([]any)
	if len(vocab) != 4 {
		t.Errorf("outcomeVocabulary = %v, want all four outcomes", vocab)
	}
	// Facets are the values actually present, per axis.
	facets, _ := full["facets"].(map[string]any)
	for _, axis := range []string{"sources", "inputActions", "outputActions", "outcomes", "reasons", "devices"} {
		if _, ok := facets[axis]; !ok {
			t.Errorf("facets missing axis %q", axis)
		}
	}
}

// A time window is a filter, and a malformed one is refused rather than
// silently ignored — a filter that quietly does nothing is this surface's
// worst failure mode.
func TestActionLogTimeWindow(t *testing.T) {
	st := newTestStore(t)
	st.ActionLog.Append(store.ActionRecord{Outcome: store.OutcomeDelivered})

	h := handleActionLog(st)
	get := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/chat/action-log?"+query, nil)
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}

	if rec := get("since=1m"); rec.Code != http.StatusOK {
		t.Errorf("since=1m: status %d, want 200", rec.Code)
	}
	if rec := get("since=-1m"); rec.Code != http.StatusOK {
		t.Errorf("since=-1m: status %d, want 200 (a window is a distance into the past)", rec.Code)
	}
	if rec := get("until=1h"); rec.Code != http.StatusOK {
		t.Errorf("until=1h: status %d, want 200", rec.Code)
	}
	if rec := get("since=not-a-time"); rec.Code != http.StatusBadRequest {
		t.Errorf("since=not-a-time: status %d, want 400", rec.Code)
	}
	if rec := get("limit=0"); rec.Code != http.StatusBadRequest {
		t.Errorf("limit=0: status %d, want 400", rec.Code)
	}
	// A window that excludes everything returns zero rather than erroring.
	var out map[string]any
	json.Unmarshal(get("since=2020-01-01T00:00:00Z&until=2020-01-02T00:00:00Z").Body.Bytes(), &out)
	if out["total"] != float64(0) {
		t.Errorf("an empty window total = %v, want 0", out["total"])
	}
}

// Nothing in a log record's own text is the evaluated string, and a hostile
// value cannot smuggle one in through an identifier field.
func TestActionLogNeverStoresEvaluatedText(t *testing.T) {
	st := newTestStore(t)
	fakeEngine(t, okEngine("clear", "clear"))
	secret := "my-passphrase-is-hunter2"
	evalWithStore(t, st, newHub(newBroker()),
		fmt.Sprintf(`{"device":"d1","streamId":"eval-d1-main","version":1,"text":%q,"voiceEnabled":true}`, secret))

	raw, err := json.Marshal(st.ActionLog.List(store.ActionLogFilter{}))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Errorf("the log carries the evaluated text:\n%s", raw)
	}
	if bytes.Contains(raw, []byte("hunter2")) {
		t.Errorf("the log carries part of the evaluated text:\n%s", raw)
	}
}
