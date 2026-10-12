package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func clientsMux() (*http.ServeMux, *Hub) {
	mux := http.NewServeMux()
	hub := newHubCore()
	registerClients(mux, hub)
	return mux, hub
}

func do(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRollCallReachesConnectedClientsAndReportsAreListed(t *testing.T) {
	mux, hub := clientsMux()
	ch, cancel := hub.subscribe("dev-1")
	defer cancel()

	if rec := do(mux, http.MethodPost, "/api/chat/clients/report",
		`{"clientId":"c1","kind":"hello","state":{"version":"1.2","build":"abc","device":"dev-1","loadAddress":"http://100.1.2.3:8787/","voiceArmed":true}}`); rec.Code != 200 {
		t.Fatalf("hello: %d %s", rec.Code, rec.Body)
	}
	rec := do(mux, http.MethodPost, "/api/chat/clients/rollcall", `{}`)
	var rc struct {
		ID        string `json:"id"`
		Connected int    `json:"connected"`
	}
	json.Unmarshal(rec.Body.Bytes(), &rc)
	if rc.Connected != 1 || !strings.HasPrefix(rc.ID, "rc-") {
		t.Fatalf("rollcall = %+v", rc)
	}
	ev := <-ch
	if ev.name != "roll_call" {
		t.Fatalf("event %q, want roll_call", ev.name)
	}
	do(mux, http.MethodPost, "/api/chat/clients/report",
		`{"clientId":"c1","kind":"report","rollCallId":"`+rc.ID+`","state":{"lastPhrase":"hello bravely","submitOutcome":"enter-sent","screenErrors":["a","b"]}}`)

	var list struct {
		Connected      int         `json:"connected"`
		LastRollCallID string      `json:"lastRollCallId"`
		Clients        []clientRow `json:"clients"`
	}
	rec = do(mux, http.MethodGet, "/api/chat/clients", "")
	json.Unmarshal(rec.Body.Bytes(), &list)
	if list.Connected != 1 || list.LastRollCallID != rc.ID || len(list.Clients) != 1 {
		t.Fatalf("list = %+v", list)
	}
	c := list.Clients[0]
	if c.LastRollCallID != rc.ID || c.State.Version != "1.2" || c.State.LastPhrase != "hello bravely" ||
		c.State.VoiceArmed == nil || !*c.State.VoiceArmed || len(c.State.ScreenErrors) != 2 {
		t.Fatalf("row did not merge hello+report: %+v", c)
	}
}

func TestClientReportValidatesAndBounds(t *testing.T) {
	mux, _ := clientsMux()
	for _, body := range []string{`{"kind":"hello"}`, `{"clientId":"x","kind":"bogus"}`, `nope`} {
		if rec := do(mux, http.MethodPost, "/api/chat/clients/report", body); rec.Code != 400 {
			t.Errorf("%s -> %d, want 400", body, rec.Code)
		}
	}
	long := strings.Repeat("x", 5000)
	do(mux, http.MethodPost, "/api/chat/clients/report", `{"clientId":"c","kind":"hello","state":{"lastPhrase":"`+long+`"}}`)
	var list struct{ Clients []clientRow }
	json.Unmarshal(do(mux, http.MethodGet, "/api/chat/clients", "").Body.Bytes(), &list)
	if got := len(list.Clients[0].State.LastPhrase); got != maxClientString {
		t.Errorf("phrase length %d, want %d", got, maxClientString)
	}
	if rec := do(mux, http.MethodGet, "/api/chat/clients/rollcall", ""); rec.Code != 405 {
		t.Errorf("GET rollcall = %d, want 405", rec.Code)
	}
}
