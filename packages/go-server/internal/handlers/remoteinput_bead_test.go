// Bead-mode handler tests (task-r887x): wire shape for mode:"bead" —
// targetless accepted, dry-run honest, failures typed. The service runs
// a fake bead backend; Talon is never touched.
package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"parlay/go-server/internal/remoteinput"
)

// fakeBeadBackend records creates and returns a scripted id.
type fakeBeadBackend struct {
	mu      sync.Mutex
	wrapper string
	nextID  string
	resolve error
	create  error
	creates []fakeBeadCall
}

type fakeBeadCall struct {
	store string
	text  string
}

func (f *fakeBeadBackend) Resolve(store string) (string, error) {
	if f.resolve != nil {
		return "", f.resolve
	}
	if f.wrapper != "" {
		return f.wrapper, nil
	}
	return "/fake/wrapper/" + store, nil
}

func (f *fakeBeadBackend) Create(store, text string) (string, string, error) {
	w, err := f.Resolve(store)
	if err != nil {
		return "", "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, fakeBeadCall{store: store, text: text})
	if f.create != nil {
		return "", w, f.create
	}
	if f.nextID != "" {
		return f.nextID, w, nil
	}
	return store + "-h1", w, nil
}

func (f *fakeBeadBackend) nCreates() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.creates)
}

// beadTestService builds a fake-Talon service with a fake bead backend.
func beadTestService(b *fakeBeadBackend) *remoteinput.Service {
	svc := remoteinput.NewService(&remoteinput.FakeTalon{}, 0, nil)
	svc.SetBeadCreator(b)
	return svc
}

func postBead(t *testing.T, svc *remoteinput.Service, url, body string) (int, []byte) {
	t.Helper()
	submit := handleRemoteInputSubmit(svc)
	req := httptest.NewRequest("POST", url, bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	submit(w, req)
	return w.Code, w.Body.Bytes()
}

func TestRemoteInputBeadSubmitNoTargetAccepted(t *testing.T) {
	beads := &fakeBeadBackend{nextID: "inbox-bead9"}
	svc := beadTestService(beads)
	defer svc.Stop()
	status := handleRemoteInputStatus(svc)

	code, body := postBead(t, svc, "/api/chat/remote-input/submit",
		`{"device":"phone-1","text":"capture ✓ this","mode":"bead"}`)
	if code != http.StatusAccepted {
		t.Fatalf("bead submit: got %d, want 202 (%s)", code, body)
	}
	var resp remoteinput.SubmitResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("submit: bad body: %v", err)
	}
	o := waitRemoteOutcome(t, status, resp.ID)
	if o.Status != remoteinput.StatusBeadCreated {
		t.Fatalf("expected bead_created, got %+v", o)
	}
	if o.BeadID != "inbox-bead9" || o.BeadStore != "inbox" {
		t.Fatalf("outcome must carry id + store: %+v", o)
	}
	if o.CapturedText != "capture ✓ this" {
		t.Fatalf("captured = %q, want exact bytes", o.CapturedText)
	}
	if o.InjectAttempted || o.Focus != remoteinput.FocusNotRequired {
		t.Fatalf("bead path: nothing typed, focus not required: %+v", o)
	}
	if beads.nCreates() != 1 {
		t.Fatalf("expected 1 create, got %d", beads.nCreates())
	}
}

func TestRemoteInputBeadStoreAndQueryParam(t *testing.T) {
	beads := &fakeBeadBackend{}
	svc := beadTestService(beads)
	defer svc.Stop()
	status := handleRemoteInputStatus(svc)

	// ?mode=bead ?store=task mirror the body fields.
	code, body := postBead(t, svc, "/api/chat/remote-input/submit?mode=bead&store=task",
		`{"device":"phone-1","text":"a task"}`)
	if code != http.StatusAccepted {
		t.Fatalf("query-param bead submit: got %d (%s)", code, body)
	}
	var resp remoteinput.SubmitResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("submit: bad body: %v", err)
	}
	o := waitRemoteOutcome(t, status, resp.ID)
	if o.Status != remoteinput.StatusBeadCreated || o.BeadStore != "task" || o.BeadID != "task-h1" {
		t.Fatalf("store selection wrong: %+v", o)
	}
}

func TestRemoteInputBeadValidation(t *testing.T) {
	beads := &fakeBeadBackend{}
	svc := beadTestService(beads)
	defer svc.Stop()

	for name, tc := range map[string]struct {
		url  string
		body string
		want string
	}{
		"unknown mode": {"/api/chat/remote-input/submit",
			`{"device":"d","text":"x","mode":"teleport"}`, "unknown mode"},
		"bad store": {"/api/chat/remote-input/submit",
			`{"device":"d","text":"x","mode":"bead","store":"../evil"}`, "invalid bead store"},
		"too long": {"/api/chat/remote-input/submit",
			`{"device":"d","text":"` + strings.Repeat("a", 2001) + `","mode":"bead"}`, "exceeds 2000"},
	} {
		code, body := postBead(t, svc, tc.url, tc.body)
		if code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, code)
		}
		if !strings.Contains(string(body), tc.want) {
			t.Errorf("%s: body must name the rule (%q): %s", name, tc.want, body)
		}
	}
	if beads.nCreates() != 0 {
		t.Fatalf("rejected submits created %d beads", beads.nCreates())
	}
}

func TestRemoteInputBeadDryRunCreatesNothing(t *testing.T) {
	beads := &fakeBeadBackend{wrapper: "/fake/wrapper/inbox"}
	svc := beadTestService(beads)
	defer svc.Stop()
	status := handleRemoteInputStatus(svc)

	code, body := postBead(t, svc, "/api/chat/remote-input/submit",
		`{"device":"phone-1","text":"would capture","mode":"bead","store":"inbox","dryRun":true}`)
	if code != http.StatusAccepted {
		t.Fatalf("dry-run bead submit: got %d (%s)", code, body)
	}
	var resp remoteinput.SubmitResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("submit: bad body: %v", err)
	}
	o := waitRemoteOutcome(t, status, resp.ID)
	if o.Status != remoteinput.StatusDryRunPassed {
		t.Fatalf("expected dry_run_passed, got %+v", o)
	}
	if !o.DryRun || o.WouldInsert != "would capture" {
		t.Fatalf("dry run must report exact bytes: %+v", o)
	}
	if o.BeadStore != "inbox" || o.BeadWrapper != "/fake/wrapper/inbox" {
		t.Fatalf("dry run must report store + wrapper: %+v", o)
	}
	if beads.nCreates() != 0 {
		t.Fatalf("dry run created %d beads", beads.nCreates())
	}
}

func TestRemoteInputBeadFailureIsTyped(t *testing.T) {
	beads := &fakeBeadBackend{
		resolve: &remoteinput.WrapperMissingError{
			Store: "inbox", Tried: []string{"/fake/a/inbox", "PATH:inbox"},
		},
	}
	svc := beadTestService(beads)
	defer svc.Stop()
	status := handleRemoteInputStatus(svc)

	code, body := postBead(t, svc, "/api/chat/remote-input/submit",
		`{"device":"phone-1","text":"lost?","mode":"bead"}`)
	if code != http.StatusAccepted {
		t.Fatalf("submit: got %d (%s)", code, body)
	}
	var resp remoteinput.SubmitResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("submit: bad body: %v", err)
	}
	o := waitRemoteOutcome(t, status, resp.ID)
	if o.Status != remoteinput.StatusBeadFailed {
		t.Fatalf("expected bead_failed, got %+v", o)
	}
	if !strings.Contains(o.Error, "wrapper not found") {
		t.Fatalf("failure must be typed: %+v", o)
	}
	if o.BeadID != "" {
		t.Fatalf("failure must never set an id: %+v", o)
	}
}

func TestRemoteInputInjectRefusalUnchanged(t *testing.T) {
	// The no-target refusal still fires for inject mode — named or
	// defaulted — while bead mode sails through targetless.
	beads := &fakeBeadBackend{}
	svc := beadTestService(beads)
	defer svc.Stop()

	for _, body := range []string{
		`{"device":"phone-1","text":"blind?"}`,
		`{"device":"phone-1","text":"blind?","mode":"inject"}`,
	} {
		code, respBody := postBead(t, svc, "/api/chat/remote-input/submit", body)
		if code != http.StatusBadRequest {
			t.Fatalf("%s: got %d, want 400", body, code)
		}
		if !strings.Contains(string(respBody), "allowUnfocused") {
			t.Fatalf("%s: refusal must name the flag: %s", body, respBody)
		}
	}

	// Unknown ids and the settle path are untouched; give the worker a
	// moment to prove nothing was queued behind the refusals.
	time.Sleep(50 * time.Millisecond)
	if beads.nCreates() != 0 {
		t.Fatalf("refusals created %d beads", beads.nCreates())
	}
}
