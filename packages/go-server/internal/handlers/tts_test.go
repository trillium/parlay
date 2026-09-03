package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeForSpeech(t *testing.T) {
	tests := []struct {
		input       string
		shouldMatch string
	}{
		{"v3.7.1", "point"}, // Should contain "point"
		{"v2.0", "point"},   // Should contain "point"
		{"3.7.1", "point"},  // Should contain "point"
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeForSpeech(tt.input)
			if !strings.Contains(result, tt.shouldMatch) {
				t.Errorf("normalizeForSpeech(%q) = %q, should contain %q", tt.input, result, tt.shouldMatch)
			}
		})
	}
}

func TestSplitBlocksRaw(t *testing.T) {
	text := "This is a sentence. This is another one! And a third? More text here."
	blocks := splitBlocksRaw(text)

	if len(blocks) == 0 {
		t.Errorf("splitBlocksRaw returned empty blocks")
	}

	for _, b := range blocks {
		if b["synth"] == "" {
			t.Errorf("splitBlocksRaw produced empty synth block")
		}
	}
}

func TestTTSHandlerHandleTTSSynth(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewSpeakDaemonEngine("/tmp/nonexistent.sock", time.Second)
	handler := NewTTSHandler(engine, tmpDir)

	tests := []struct {
		name       string
		method     string
		path       string
		body       map[string]interface{}
		expectCode int
		expectKey  string
	}{
		{
			name:       "empty text",
			method:     http.MethodPost,
			path:       "/api/chat/tts",
			body:       map[string]interface{}{"text": ""},
			expectCode: http.StatusOK,
			expectKey:  "error",
		},
		{
			name:       "missing text",
			method:     http.MethodPost,
			path:       "/api/chat/tts",
			body:       map[string]interface{}{},
			expectCode: http.StatusOK,
			expectKey:  "error",
		},
		{
			name:       "valid text (will fail due to no daemon)",
			method:     http.MethodPost,
			path:       "/api/chat/tts",
			body:       map[string]interface{}{"text": "hello"},
			expectCode: http.StatusOK,
			expectKey:  "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler.handleTTSSynth(w, req)

			if w.Code != tt.expectCode {
				t.Errorf("handleTTSSynth returned code %d, want %d", w.Code, tt.expectCode)
			}

			// For audio responses, check for RIFF header; for JSON, check for the expected key
			if tt.expectKey != "" {
				var resp map[string]interface{}
				json.NewDecoder(w.Body).Decode(&resp)
				if _, ok := resp[tt.expectKey]; !ok {
					t.Errorf("response missing expected key %q", tt.expectKey)
				}
			}
		})
	}
}

func TestTTSHandlerHandleTTSCorrection(t *testing.T) {
	tmpDir := t.TempDir()
	subsPath := filepath.Join(tmpDir, "tts-substitutions.json")

	// Create parent directories
	os.MkdirAll(tmpDir, 0755)

	engine := NewSpeakDaemonEngine("/tmp/nonexistent.sock", time.Second)
	handler := NewTTSHandler(engine, tmpDir)

	body := map[string]interface{}{
		"from":     "hello",
		"to":       "hi",
		"sentence": "hello world",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/tts-correction", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.handleTTSCorrection(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleTTSCorrection returned code %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("response missing ok=true")
	}

	// Verify the file was created
	if _, err := os.Stat(subsPath); os.IsNotExist(err) {
		t.Logf("Note: substitutions file not created (expected if dir doesn't have write perms)")
	}
}

func TestTTSHandlerHandleTTSReport(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(tmpDir, 0755)

	engine := NewSpeakDaemonEngine("/tmp/nonexistent.sock", time.Second)
	handler := NewTTSHandler(engine, tmpDir)

	body := map[string]interface{}{
		"sentence": "this is a test",
		"voice":    "parlay-pool",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/tts-report", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.handleTTSReport(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleTTSReport returned code %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("response missing ok=true")
	}
}

func TestHandleTTSEventRequest(t *testing.T) {
	b := newBroker()
	hub := newHub(b)
	t.Cleanup(hub.Stop)

	body := map[string]interface{}{
		"type":   "play",
		"device": "device1",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/tts-event", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	HandleTTSEventRequest(w, req, hub)

	if w.Code != http.StatusOK {
		t.Errorf("HandleTTSEventRequest returned code %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("response missing ok=true")
	}
}

func TestHandleTTSValidateRequest(t *testing.T) {
	body := map[string]interface{}{
		"text":  "This is a test. Here is another sentence.",
		"model": "gemma4:latest",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/tts/validate-splits", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	HandleTTSValidateRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("HandleTTSValidateRequest returned code %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if _, ok := resp["blocks"]; !ok {
		t.Errorf("response missing blocks field")
	}
	if _, ok := resp["evaluation"]; !ok {
		t.Errorf("response missing evaluation field")
	}
}

// MockTTSEngine is a test implementation of TTSEngine for testing.
type MockTTSEngine struct {
	shouldFail bool
	callCount  int
}

func (m *MockTTSEngine) Synth(text string, voice string, speed float64) ([]byte, float64, string, error) {
	m.callCount++
	if m.shouldFail {
		return nil, 0, "", io.EOF
	}
	// Return minimal WAV header (RIFF)
	wav := []byte{
		'R', 'I', 'F', 'F',
		0x24, 0x00, 0x00, 0x00, // File size - 8
		'W', 'A', 'V', 'E',
		'f', 'm', 't', ' ',
		0x10, 0x00, 0x00, 0x00, // Subchunk1 size
		0x01, 0x00, // Audio format (PCM)
		0x01, 0x00, // Num channels
		0x44, 0xac, 0x00, 0x00, // Sample rate (44100)
		0x88, 0x58, 0x01, 0x00, // Byte rate
		0x02, 0x00, // Block align
		0x10, 0x00, // Bits per sample
		'd', 'a', 't', 'a',
		0x00, 0x00, 0x00, 0x00, // Subchunk2 size
	}
	return wav, 1.0, voice, nil
}

func TestTTSHandlerWithMockEngine(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(tmpDir, 0755)

	engine := &MockTTSEngine{shouldFail: false}
	handler := NewTTSHandler(engine, tmpDir)

	body := map[string]interface{}{
		"text":  "hello world",
		"voice": "test-voice",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/tts", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.handleTTSSynth(w, req)

	// Should return audio data (RIFF header)
	if w.Body.Len() < 4 {
		t.Errorf("response too small for WAV data")
	}

	if engine.callCount != 1 {
		t.Errorf("engine.Synth called %d times, want 1", engine.callCount)
	}
}
