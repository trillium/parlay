package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"parlay/go-server/internal/remoteinput"
)

func TestRemoteInputActionDryRunAndQuit(t *testing.T) {
	fake := &remoteinput.FakeTalon{}
	svc := remoteinput.NewService(fake, 0, nil)
	defer svc.Stop()
	status := handleRemoteInputStatus(svc)

	code, body := postBead(t, svc, "/api/chat/remote-input/submit",
		`{"device":"phone-1","mode":"action","action":"slide_next","dryRun":true}`)
	if code != http.StatusAccepted {
		t.Fatalf("dry-run action: got %d, want 202 (%s)", code, body)
	}
	var resp remoteinput.SubmitResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("submit: bad body: %v", err)
	}
	o := waitRemoteOutcome(t, status, resp.ID)
	if o.Status != remoteinput.StatusDryRunPassed || o.WouldRun != "actions.user.slide_next()" {
		t.Fatalf("dry run outcome: %+v", o)
	}
	if len(fake.RunActions) != 0 || len(fake.Inserts) != 0 {
		t.Fatalf("dry run touched Talon: %+v", fake)
	}

	code, body = postBead(t, svc, "/api/chat/remote-input/submit",
		`{"device":"phone-1","mode":"action","action":"quit"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("quit: got %d, want 400 (%s)", code, body)
	}
	if !strings.Contains(string(body), "not allowed") {
		t.Fatalf("quit body must say not allowed: %s", body)
	}
	if len(fake.RunActions) != 0 {
		t.Fatalf("quit reached Talon: %v", fake.RunActions)
	}

	code, body = postBead(t, svc, "/api/chat/remote-input/submit?mode=action&action=slide_previous",
		`{"device":"phone-1"}`)
	if code != http.StatusAccepted {
		t.Fatalf("query action: got %d, want 202 (%s)", code, body)
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("submit: bad body: %v", err)
	}
	o = waitRemoteOutcome(t, status, resp.ID)
	if o.Status != remoteinput.StatusInjected || o.WouldRun != "actions.user.slide_previous()" {
		t.Fatalf("live previous: %+v", o)
	}
	if len(fake.RunActions) != 1 || fake.RunActions[0] != "slide_previous" {
		t.Fatalf("RunActions = %v, want [slide_previous]", fake.RunActions)
	}
	if len(fake.Inserts) != 0 {
		t.Fatalf("action mode inserted %v", fake.Inserts)
	}
}
