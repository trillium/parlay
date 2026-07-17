package main

import (
	"encoding/json"
	"testing"
)

// evalWith runs one eval carrying a per-request command override (raw JSON).
func evalWith(e *Engine, text string, ver int64, commands string) EvalResponse {
	return e.Eval(EvalRequest{
		StreamID: "override-test", Version: ver, Text: text,
		VoiceEnabled: true, Reason: "input",
		Commands: json.RawMessage(commands),
	})
}

// TestPerRequestOverrideReplacesSet proves a valid EvalRequest.commands wholly
// replaces the engine's set FOR THAT REQUEST, and that a request without an override
// still sees the live (embedded) set — the request layer does not persist.
func TestPerRequestOverrideReplacesSet(t *testing.T) {
	e := NewEngine()
	override := manifestWithClearPhrase("req", "smash it completely")

	// With the override, its phrase fires and the embedded phrase does not.
	if got := evalWith(e, "smash it completely", 1, override).Fired; got != "clear" {
		t.Fatalf("override phrase should fire, got %q", got)
	}
	if got := evalWith(e, "change inside input", 2, override).Fired; got != "" {
		t.Fatalf("override replaces the embedded set for this request; embedded phrase must not fire, got %q", got)
	}

	// A request WITHOUT the override still evaluates against the live embedded set.
	if got := eval(e, "change inside input", 3, nil).Fired; got != "clear" {
		t.Fatalf("no override: embedded set must be live, got %q", got)
	}
	if got := eval(e, "smash it completely", 4, nil).Fired; got != "" {
		t.Fatalf("no override: the request-only phrase must not persist, got %q", got)
	}
}

// TestPerRequestOverrideInvalidIgnored proves an invalid override is ignored and the
// request falls through to the live set (fail-closed, never a request failure).
func TestPerRequestOverrideInvalidIgnored(t *testing.T) {
	e := NewEngine()
	cases := []struct {
		name     string
		override string
	}{
		{"garbage", "{ not json at all"},
		{"null literal", "null"},
		{"empty object", "{}"},
		{"empty command set", `{"schema":"parlay.commands/v1","version":"x","commands":[]}`},
		{"unknown verb", `{"schema":"parlay.commands/v1","version":"x","commands":[{"id":"z","phrases":["boop"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[{"verb":"teleport"}]}}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := evalWith(e, "change inside input", 1, c.override)
			if r.Fired != "clear" {
				t.Fatalf("invalid override should be ignored and fall through to embedded (fire clear), got %q (%v)", r.Fired, verbs(r))
			}
		})
	}
}

// TestPrecedenceRequestOverFile proves the full precedence chain: a loaded file set
// beats the embedded default, and a per-request override beats the file set — for
// that request only.
func TestPrecedenceRequestOverFile(t *testing.T) {
	e := NewEngine()

	// File layer A (loaded via SetCommands): clear on "file phrase alpha".
	manA, err := parseManifest([]byte(manifestWithClearPhrase("A", "file phrase alpha")))
	if err != nil {
		t.Fatal(err)
	}
	e.SetCommands(manA)

	// Without an override, the file set is live.
	if got := eval(e, "file phrase alpha", 1, nil).Fired; got != "clear" {
		t.Fatalf("file set should be live, got %q", got)
	}
	if got := eval(e, "change inside input", 2, nil).Fired; got != "" {
		t.Fatalf("embedded phrase must be gone after file load, got %q", got)
	}

	// A per-request override B beats the file set for this request.
	override := manifestWithClearPhrase("B", "request phrase beta")
	if got := evalWith(e, "request phrase beta", 3, override).Fired; got != "clear" {
		t.Fatalf("request override should win, got %q", got)
	}
	if got := evalWith(e, "file phrase alpha", 4, override).Fired; got != "" {
		t.Fatalf("override replaces the file set for this request; file phrase must not fire, got %q", got)
	}

	// After the override request, the file set is still live (override did not persist).
	if got := eval(e, "file phrase alpha", 5, nil).Fired; got != "clear" {
		t.Fatalf("file set must remain live after an override request, got %q", got)
	}
}
