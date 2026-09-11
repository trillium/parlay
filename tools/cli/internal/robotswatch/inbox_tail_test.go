package robotswatch

import "testing"

// inbox-tail: PUSH fast path for the inbox store — mirrors robots-tail.
// A byte-offset tailer of ~/data/inbox/events.jsonl parses each new line
// for an inbox bead id and calls inbox-dispatch immediately. These tests
// pin the parse + offset behavior before the command exists.

func TestParseInboxCreatedIDExtractsInboxID(t *testing.T) {
	id, ok := parseInboxCreatedID(`{"id":"inbox-eq2s","created_at":"2026-09-11T16:38:18Z","source":"inbox-create"}`)
	if !ok || id != "inbox-eq2s" {
		t.Fatalf("got (%q, %v)", id, ok)
	}
}

func TestParseInboxCreatedIDRejectsInvalid(t *testing.T) {
	cases := []string{
		`{"id":"robots-aaa"}`,
		`{"id":"task-abc"}`,
		"not json",
		`{"created_at":"x"}`,
		`{"id":"inbox-"}`,
		`{"id":"inbox-ABC"}`,
	}
	for _, c := range cases {
		if _, ok := parseInboxCreatedID(c); ok {
			t.Fatalf("expected reject for %q", c)
		}
	}
}

func TestInboxEventsPathDefaultsToInboxEmitFile(t *testing.T) {
	t.Setenv("INBOX_EVENTS_FILE", "")
	if got := inboxEventsPath(); !endsWith(got, "data/inbox/events.jsonl") {
		t.Fatalf("got %q", got)
	}
}

func TestInboxEventsPathHonorsEnv(t *testing.T) {
	t.Setenv("INBOX_EVENTS_FILE", "/tmp/custom-inbox-events.jsonl")
	if got := inboxEventsPath(); got != "/tmp/custom-inbox-events.jsonl" {
		t.Fatalf("got %q", got)
	}
}

func TestInboxOffsetPathIsSeparateFromRobotsTail(t *testing.T) {
	if inboxOffsetPath() == offsetPath() {
		t.Fatalf("inbox tail must not share the robots tail offset file")
	}
}

func endsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
