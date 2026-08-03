package help

import (
	"strings"
	"testing"
)

func TestUsageSubstitutesServer(t *testing.T) {
	got := Usage("http://example.test:1234")
	if !strings.Contains(got, "http://example.test:1234") {
		t.Errorf("Usage() missing server URL: %q", got)
	}
	if strings.Contains(got, "{{SERVER}}") {
		t.Errorf("Usage() left the placeholder unsubstituted: %q", got)
	}
}

func TestLookupKnownCommand(t *testing.T) {
	text, ok := Lookup("status")
	if !ok {
		t.Fatal("expected help text for status")
	}
	if !strings.Contains(text, "parlay status") {
		t.Errorf("Lookup(status) = %q", text)
	}
}

func TestLookupUnknownCommand(t *testing.T) {
	if _, ok := Lookup("does-not-exist"); ok {
		t.Error("expected ok=false for an unregistered command")
	}
}
