package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"parlay/go-server/internal/store"
)

func TestHandleSettingsGetReturnsDefaultsWhenUnset(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/chat/parlay/settings", nil)
	rec := httptest.NewRecorder()
	handleSettings(newTestStore(t))(rec, req)

	var got store.ParlaySettings
	decodeBody(t, rec, &got)
	want := store.DefaultSettings()
	if got.PanelSide != want.PanelSide || got.VoiceSettleMs != want.VoiceSettleMs {
		t.Errorf("GET settings = %+v, want defaults %+v", got, want)
	}
}

func TestHandleSettingsPutReplacesWholeDocument(t *testing.T) {
	st := newTestStore(t)
	h := handleSettings(st)

	s := store.DefaultSettings()
	s.PanelSide = "left"
	s.VoiceEnabled = true
	rec := putJSON(t, h, s)
	var put store.ParlaySettings
	decodeBody(t, rec, &put)
	if put.PanelSide != "left" || !put.VoiceEnabled {
		t.Fatalf("PUT response = %+v, want PanelSide=left VoiceEnabled=true", put)
	}

	// A follow-up PUT omitting fields replaces them with zero values —
	// whole-document replace, not a patch.
	rec2 := putJSON(t, h, map[string]any{"panelSide": "right"})
	var put2 store.ParlaySettings
	decodeBody(t, rec2, &put2)
	if put2.PanelSide != "right" || put2.VoiceEnabled {
		t.Errorf("second PUT response = %+v, want PanelSide=right VoiceEnabled=false (zeroed, not merged)", put2)
	}
	if got := st.Settings.Get(); got.VoiceEnabled {
		t.Errorf("Settings.Get() after second PUT = %+v, want VoiceEnabled=false", got)
	}
}

func TestHandleSettingsInvalidJSONIs400(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/api/chat/parlay/settings", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	handleSettings(newTestStore(t))(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleSettingsWrongMethodIs405(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/api/chat/parlay/settings", nil)
	rec := httptest.NewRecorder()
	handleSettings(newTestStore(t))(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
