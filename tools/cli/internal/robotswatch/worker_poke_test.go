package robotswatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInboxIsPiZone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels []string
		want   bool
	}{
		{"un zoned", nil, true},
		{"pi", []string{"zone:pi"}, true},
		{"default", []string{"zone:default"}, true},
		{"specialized", []string{"zone:parlay"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inboxIsPiZone(tc.labels); got != tc.want {
				t.Fatalf("inboxIsPiZone(%v) = %v, want %v", tc.labels, got, tc.want)
			}
		})
	}
}

func TestInboxClosedPokesWorkerWithoutNotifySubscriber(t *testing.T) {
	state := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", state)
	t.Setenv("PARLAY_INBOX_DISPATCH", "")

	bin := t.TempDir()
	capture := filepath.Join(bin, "args")
	parlay := filepath.Join(bin, "parlay")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"" + capture + "\"\n"
	if err := os.WriteFile(parlay, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	handleRequestClosed(RouteEvent{Store: "inbox", Kind: EventClosed, ID: "inbox-next"}, Bead{ID: "inbox-next", Status: "closed"}, false)

	body, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("worker poke was not sent: %v", err)
	}
	got := string(body)
	for _, want := range []string{"send", "--pi-inbox", "INBOX_POKE v1: an inbox item completed"} {
		if !strings.Contains(got, want) {
			t.Errorf("poke args missing %q in %q", want, got)
		}
	}
}
