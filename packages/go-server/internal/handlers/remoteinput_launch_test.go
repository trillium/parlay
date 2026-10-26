package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"parlay/go-server/internal/remoteinput"
)

func launchHarness(t *testing.T) (http.HandlerFunc, *remoteinput.FakeTalon, *remoteinput.FakeLauncher) {
	t.Helper()
	ft := &remoteinput.FakeTalon{TargetsList: []remoteinput.Target{{Name: "Notes"}, {Name: "Safari", HasWindows: true}}}
	fl := &remoteinput.FakeLauncher{}
	svc := remoteinput.NewService(ft, 0, nil)
	t.Cleanup(svc.Stop)
	svc.SetLauncher(fl)
	return handleRemoteInputLaunch(svc), ft, fl
}

func postLaunch(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/chat/remote-input/launch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func TestRemoteInputLaunchHappyPath(t *testing.T) {
	h, ft, fl := launchHarness(t)
	w := postLaunch(h, `{"device":"d","app":"notes"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
	var got remoteinput.LaunchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "focused" || got.App != "Notes" || len(fl.Calls) != 1 || len(ft.Inserts) != 0 {
		t.Fatalf("resp=%+v launches=%v inserts=%v", got, fl.Calls, ft.Inserts)
	}
}

func TestRemoteInputLaunchDryRunPerformsNothing(t *testing.T) {
	h, ft, fl := launchHarness(t)
	for _, target := range []string{"/api/chat/remote-input/launch", "/api/chat/remote-input/launch?dryRun=1"} {
		body := `{"device":"d","app":"Notes","dryRun":true}`
		if strings.Contains(target, "?") {
			body = `{"device":"d","app":"Notes"}`
		}
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"dry_run_passed"`) {
			t.Fatalf("%s: got %d: %s", target, w.Code, w.Body)
		}
	}
	if len(fl.Calls)+len(ft.FocusAppCalls) != 0 {
		t.Fatal("dry run performed an action")
	}
}

func TestRemoteInputLaunchErrors(t *testing.T) {
	h, _, fl := launchHarness(t)
	for body, want := range map[string]int{
		`{"device":"d","app":"Nope"}`: http.StatusNotFound,
		`{"app":"Notes"}`:             http.StatusBadRequest,
		`{"device":"d"}`:              http.StatusBadRequest,
	} {
		w := postLaunch(h, body)
		if w.Code != want || !strings.Contains(w.Body.String(), "error") {
			t.Fatalf("%s: got %d %s, want %d JSON error", body, w.Code, w.Body, want)
		}
	}
	if len(fl.Calls) != 0 {
		t.Fatal("rejected requests must not launch")
	}
	fl.Err = errors.New("boom")
	if w := postLaunch(h, `{"device":"d","app":"Notes"}`); w.Code != http.StatusInternalServerError {
		t.Fatalf("launch failure: got %d, want 500", w.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/chat/remote-input/launch", nil)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: got %d, want 405", w.Code)
	}
}
