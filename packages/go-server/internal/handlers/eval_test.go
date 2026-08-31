package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleEvalMissingDevice(t *testing.T) {
	// Use a nil hub since we're just testing the request validation
	var hub *Hub
	handler := handleEval(hub)

	body := evalRequest{
		StreamID: "test-stream",
		Text:     "test",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/chat/eval", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if errorMsg, ok := resp["error"].(string); !ok || errorMsg == "" {
		t.Errorf("expected error message, got %v", resp["error"])
	}
}

func TestHandleEvalWithDevice(t *testing.T) {
	// This is a basic test that checks the request is accepted
	// A full test would need to mock the eval engine
	var hub *Hub // Use nil hub since we're testing request validation
	handler := handleEval(hub)

	body := evalRequest{
		StreamID: "test-stream",
		Text:     "test",
		Device:   "test-device",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/chat/eval", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// This will fail because the eval engine is not running, but we can check
	// that the device was registered in the streamDeviceMap
	handler(w, req)

	// Check that the device was registered
	device, ok := deviceForStream("test-stream")

	if !ok || device != "test-device" {
		t.Errorf("expected device to be registered for stream, got %v", device)
	}
}

func TestHandleEvalPushMissingStream(t *testing.T) {
	hub := newHubCore()
	handler := handleEvalPush(hub)

	body := evalPushRequest{
		Seq:         1,
		BaseVersion: 0,
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/chat/eval-push", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing streamId, got %d", w.Code)
	}
}

func TestHandleEvalPushUnknownStream(t *testing.T) {
	hub := newHubCore()
	handler := handleEvalPush(hub)

	body := evalPushRequest{
		StreamID:    "unknown-stream",
		Seq:         1,
		BaseVersion: 0,
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/chat/eval-push", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown stream, got %d", w.Code)
	}
}

func TestHandleEvalPushWithKnownStream(t *testing.T) {
	// Register a stream first
	streamDeviceMapMu.Lock()
	streamDeviceMap["test-stream"] = "test-device"
	streamDeviceMapMu.Unlock()
	defer func() {
		streamDeviceMapMu.Lock()
		delete(streamDeviceMap, "test-stream")
		streamDeviceMapMu.Unlock()
	}()

	hub := newHubCore()
	handler := handleEvalPush(hub)

	body := evalPushRequest{
		StreamID:    "test-stream",
		Seq:         1,
		BaseVersion: 0,
		Action:      map[string]interface{}{"type": "test"},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/chat/eval-push", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode(&resp)

	if ok, _ := resp["ok"].(bool); !ok {
		t.Errorf("expected ok:true, got %v", resp)
	}
}
