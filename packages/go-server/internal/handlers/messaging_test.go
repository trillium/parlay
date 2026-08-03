package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"parlay/go-server/internal/store"
)

func postJSON(t *testing.T, h http.HandlerFunc, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("unmarshal response %q: %v", rec.Body.String(), err)
	}
}

func TestHandleSendRequiresTextOrImages(t *testing.T) {
	st := newTestStore(t)
	rec := postJSON(t, handleSend(st, newBroker()), map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (error-on-200 convention)", rec.Code)
	}
	var got map[string]string
	decodeBody(t, rec, &got)
	if got["error"] == "" {
		t.Errorf("body = %v, want an error field", got)
	}
}

func TestHandleSendDefaultsToMainChannel(t *testing.T) {
	st := newTestStore(t)
	rec := postJSON(t, handleSend(st, newBroker()), map[string]any{"text": "hi"})
	var got okIDResponse
	decodeBody(t, rec, &got)
	if !got.OK || got.ID == "" {
		t.Fatalf("response = %+v, want ok=true with an id", got)
	}
	history := st.Messages.History(0)
	if len(history) != 1 || history[0].Channel != "" || history[0].Role != "user" {
		t.Errorf("stored message = %+v, want role=user channel=\"\"", history)
	}
}

func TestHandleSendUsesToAgentAsChannel(t *testing.T) {
	st := newTestStore(t)
	postJSON(t, handleSend(st, newBroker()), map[string]any{"text": "hi", "toAgent": "c0"})
	history := st.Messages.History(0)
	if len(history) != 1 || history[0].Channel != "c0" {
		t.Errorf("stored message channel = %q, want c0", history[0].Channel)
	}
}

func TestHandleSendInvalidJSONIs400(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	handleSend(newTestStore(t), newBroker())(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleSendWrongMethodIs405(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handleSend(newTestStore(t), newBroker())(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleReplyRequiresTextAndAgent(t *testing.T) {
	st := newTestStore(t)
	rec := postJSON(t, handleReply(st, newBroker()), map[string]any{"text": "hi"})
	var got map[string]string
	decodeBody(t, rec, &got)
	if rec.Code != http.StatusOK || got["error"] == "" {
		t.Errorf("status=%d body=%v, want 200 with an error field", rec.Code, got)
	}
}

func TestHandleReplyStoresAgentMessageOnItsOwnChannel(t *testing.T) {
	st := newTestStore(t)
	rec := postJSON(t, handleReply(st, newBroker()), map[string]any{"text": "yo", "agent": "c1", "name": "Firstmate"})
	var got okIDResponse
	decodeBody(t, rec, &got)
	if !got.OK {
		t.Fatalf("response = %+v, want ok=true", got)
	}
	history := st.Messages.History(0)
	if len(history) != 1 || history[0].Role != "agent" || history[0].Channel != "c1" || history[0].From != "Firstmate" {
		t.Errorf("stored message = %+v, want role=agent channel=c1 from=Firstmate", history[0])
	}
}

func TestHandleAlertBroadcastsToAllWhenAgentsOmitted(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Registry.Upsert(store.AgentInfo{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Registry.Upsert(store.AgentInfo{ID: "b"}); err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, handleAlert(st, newBroker()), map[string]any{"text": "broadcast"})
	var got alertResponse
	decodeBody(t, rec, &got)
	if !got.OK || got.Channels != 2 {
		t.Errorf("response = %+v, want ok=true channels=2", got)
	}
	if len(st.Messages.History(0)) != 2 {
		t.Errorf("history len = %d, want 2 (one alert per registered agent)", len(st.Messages.History(0)))
	}
}

func TestHandleAlertTargetsOnlySpecifiedAgents(t *testing.T) {
	st := newTestStore(t)
	rec := postJSON(t, handleAlert(st, newBroker()), map[string]any{"text": "hi", "agents": []string{"x", "y", "z"}})
	var got alertResponse
	decodeBody(t, rec, &got)
	if got.Channels != 3 {
		t.Errorf("channels = %d, want 3", got.Channels)
	}
	for _, m := range st.Messages.History(0) {
		if m.Type != "alert" {
			t.Errorf("message type = %q, want alert", m.Type)
		}
	}
}

func TestHandleAlertExplicitEmptyAgentsTargetsNobody(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Registry.Upsert(store.AgentInfo{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, handleAlert(st, newBroker()), map[string]any{"text": "hi", "agents": []string{}})
	var got alertResponse
	decodeBody(t, rec, &got)
	if got.Channels != 0 {
		t.Errorf("channels = %d, want 0 for an explicit empty agents list", got.Channels)
	}
}

func TestHandleMessageRequiresChannelAndText(t *testing.T) {
	rec := postJSON(t, handleMessage(newTestStore(t), newBroker()), map[string]any{"channel": "c0"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (non-2xx convention, not the error-on-200 group)", rec.Code)
	}
}

func TestHandleMessageStoresRelayedMessage(t *testing.T) {
	st := newTestStore(t)
	rec := postJSON(t, handleMessage(st, newBroker()), map[string]any{"channel": "c0", "role": "agent", "text": "digest"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	history := st.Messages.History(0)
	if len(history) != 1 || history[0].Channel != "c0" || history[0].Text != "digest" {
		t.Errorf("stored message = %+v, want channel=c0 text=digest", history)
	}
}

func TestHandleHistoryReturnsOldestFirstAndRespectsLimit(t *testing.T) {
	st := newTestStore(t)
	for _, text := range []string{"one", "two", "three"} {
		if _, err := st.Messages.Append(store.ChatMessage{Role: "user", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/chat/history?limit=2", nil)
	rec := httptest.NewRecorder()
	handleHistory(st)(rec, req)

	var got []store.ChatMessage
	decodeBody(t, rec, &got)
	if len(got) != 2 || got[0].Text != "two" || got[1].Text != "three" {
		t.Errorf("history = %+v, want last 2 messages oldest-first [two three]", got)
	}
}
