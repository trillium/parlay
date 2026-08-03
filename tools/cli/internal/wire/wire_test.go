package wire

import (
	"encoding/json"
	"testing"
)

func TestChatMessageDecodesServerShape(t *testing.T) {
	raw := `{"id":"1","role":"agent","ts":"2026-08-01T12:00:00Z","text":"hi","channel":"mayor"}`
	var m ChatMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.ID != "1" || m.Role != "agent" || m.Channel != "mayor" {
		t.Errorf("decoded = %+v", m)
	}
}

func TestSubscribersInfoDecodesNullChannel(t *testing.T) {
	raw := `{"poll":{"count":1,"channels":[{"channel":null,"id":"x"}]}}`
	var s SubscribersInfo
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if s.Poll == nil || len(s.Poll.Channels) != 1 {
		t.Fatalf("decoded = %+v", s)
	}
	if s.Poll.Channels[0].Channel != "" {
		t.Errorf("Channel = %q, want empty string for JSON null", s.Poll.Channels[0].Channel)
	}
	if s.Poll.Channels[0].ID != "x" {
		t.Errorf("ID = %q, want x", s.Poll.Channels[0].ID)
	}
}
