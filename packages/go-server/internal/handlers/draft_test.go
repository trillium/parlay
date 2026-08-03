package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"parlay/go-server/internal/store"
)

// putJSON mirrors messaging_test.go's postJSON but issues a PUT — needed for
// this package's GET/PUT-combined handlers (handleDraft, handleSettings),
// where a POST body would 405 rather than reach the write path.
func putJSON(t *testing.T, h http.HandlerFunc, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestHandleDraftGetEmptyByDefault(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/chat/draft", nil)
	rec := httptest.NewRecorder()
	handleDraft(newTestStore(t))(rec, req)

	var got store.Draft
	decodeBody(t, rec, &got)
	if got.Text != "" {
		t.Errorf("GET draft on fresh store = %+v, want empty text", got)
	}
}

func TestHandleDraftPutThenGetRoundTrips(t *testing.T) {
	st := newTestStore(t)
	h := handleDraft(st)

	rec := putJSON(t, h, map[string]any{"text": "hello", "clientId": "device-1"})
	var put store.Draft
	decodeBody(t, rec, &put)
	if put.Text != "hello" || put.ClientID != "device-1" {
		t.Fatalf("PUT response = %+v, want Text=hello ClientID=device-1", put)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/chat/draft", nil)
	rec2 := httptest.NewRecorder()
	h(rec2, req)
	var got store.Draft
	decodeBody(t, rec2, &got)
	if got.Text != "hello" {
		t.Errorf("GET after PUT = %+v, want Text=hello", got)
	}
}

func TestHandleDraftPutClearsWithEmptyText(t *testing.T) {
	st := newTestStore(t)
	h := handleDraft(st)
	putJSON(t, h, map[string]any{"text": "something"})
	putJSON(t, h, map[string]any{"text": ""})

	if got := st.Drafts.Get().Text; got != "" {
		t.Errorf("Drafts.Get().Text after clearing PUT = %q, want empty", got)
	}
}

func TestHandleDraftInvalidJSONIs400(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/api/chat/draft", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	handleDraft(newTestStore(t))(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleDraftWrongMethodIs405(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/api/chat/draft", nil)
	rec := httptest.NewRecorder()
	handleDraft(newTestStore(t))(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
