package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"parlay/go-server/internal/remoteinput"
)

const talonTestToken = "tok-123"

func talonGuard(t *testing.T, run, inject bool, commands, apps []string, token string) remoteinput.Guardrails {
	t.Helper()
	g, err := remoteinput.NewGuardrails(run, inject, commands, apps, token)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

type talonReq struct {
	remote, auth string
}

var okReq = talonReq{remote: "100.101.102.103:4444", auth: "Bearer " + talonTestToken}

func callTalon(h http.HandlerFunc, rq talonReq, body string) (int, []byte) {
	req := httptest.NewRequest("POST", "/x", bytes.NewBufferString(body))
	req.RemoteAddr = rq.remote
	if rq.auth != "" {
		req.Header.Set("Authorization", rq.auth)
	}
	w := httptest.NewRecorder()
	h(w, req)
	return w.Code, w.Body.Bytes()
}

// commandPassCases pins, per catalogue command, the request that must pass
// and the exact snippet it must resolve to. TestEveryAllowListedCommandHasATest
// fails if a command is allow-listed (default or catalogue) without an entry
// here: adding a capability without a test is a build failure. Widening the
// default allow-list needs captain review.
var commandPassCases = map[string]string{
	"slide_next":     "actions.user.slide_next()",
	"slide_previous": "actions.user.slide_previous()",
}

func TestEveryAllowListedCommandHasATest(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range append(remoteinput.CatalogCommands(), remoteinput.DefaultCommands...) {
		seen[name] = true
	}
	for name := range seen {
		if _, ok := commandPassCases[name]; !ok {
			t.Errorf("allow-listed command %q has no pass case in commandPassCases — add one", name)
		}
	}
	for name := range commandPassCases {
		if !seen[name] {
			t.Errorf("commandPassCases has %q but it is not allow-listed: stale test", name)
		}
	}
}

func TestAllowedCommandPasses(t *testing.T) {
	for name, snippet := range commandPassCases {
		t.Run(name, func(t *testing.T) {
			fake := &remoteinput.FakeTalon{}
			svc := remoteinput.NewService(fake, 0, nil)
			defer svc.Stop()
			g := talonGuard(t, true, false, []string{name}, nil, talonTestToken)
			h := handleTalonRun(svc, remoteInputSeam{}, g)

			code, body := callTalon(h, okReq, `{"command":"`+name+`","dryRun":true}`)
			if code != http.StatusAccepted {
				t.Fatalf("dry run: %d %s", code, body)
			}
			var resp remoteinput.SubmitResponse
			_ = json.Unmarshal(body, &resp)
			o := waitRemoteOutcome(t, handleRemoteInputStatus(svc), resp.ID)
			if o.Status != remoteinput.StatusDryRunPassed || o.WouldRun != snippet {
				t.Fatalf("outcome: %+v", o)
			}

			code, body = callTalon(h, okReq, `{"command":"`+name+`"}`)
			if code != http.StatusAccepted {
				t.Fatalf("live: %d %s", code, body)
			}
			_ = json.Unmarshal(body, &resp)
			o = waitRemoteOutcome(t, handleRemoteInputStatus(svc), resp.ID)
			if o.Status != remoteinput.StatusInjected || len(fake.RunActions) != 1 || fake.RunActions[0] != name {
				t.Fatalf("live outcome %+v runs %v", o, fake.RunActions)
			}
		})
	}
}

func TestTalonRunRefusals(t *testing.T) {
	fake := &remoteinput.FakeTalon{}
	svc := remoteinput.NewService(fake, 0, nil)
	defer svc.Stop()
	g := talonGuard(t, true, false, []string{"slide_next"}, nil, talonTestToken)
	h := handleTalonRun(svc, remoteInputSeam{}, g)
	good := `{"command":"slide_next"}`

	cases := []struct {
		name string
		rq   talonReq
		body string
		want int
	}{
		{"disallowed command", okReq, `{"command":"quit"}`, http.StatusForbidden},
		{"catalogue command not in config", okReq, `{"command":"slide_previous"}`, http.StatusForbidden},
		{"snippet-shaped command", okReq, `{"command":"actions.user.slide_next(); import os"}`, http.StatusForbidden},
		{"shell-shaped command", okReq, `{"command":"rm -rf /"}`, http.StatusForbidden},
		{"empty command", okReq, `{}`, http.StatusForbidden},
		{"unknown field", okReq, `{"command":"slide_next","shell":"id"}`, http.StatusBadRequest},
		{"missing token", talonReq{remote: okReq.remote}, good, http.StatusUnauthorized},
		{"wrong token", talonReq{remote: okReq.remote, auth: "Bearer nope"}, good, http.StatusUnauthorized},
		{"non-bearer scheme", talonReq{remote: okReq.remote, auth: "Basic " + talonTestToken}, good, http.StatusUnauthorized},
		{"public origin", talonReq{remote: "8.8.8.8:5000", auth: okReq.auth}, good, http.StatusForbidden},
		{"lan origin", talonReq{remote: "192.168.1.9:5000", auth: okReq.auth}, good, http.StatusForbidden},
	}
	for _, c := range cases {
		if code, body := callTalon(h, c.rq, c.body); code != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, code, c.want, body)
		}
	}
	if len(fake.RunActions) != 0 || len(fake.Inserts) != 0 {
		t.Fatalf("a refused request reached Talon: %+v", fake)
	}
}

func TestTalonRunOffByDefault(t *testing.T) {
	g, err := remoteinput.GuardrailsFromEnv(func(k string) (string, bool) {
		if k == remoteinput.TalonTokenEnv {
			return talonTestToken, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &remoteinput.FakeTalon{}
	svc := remoteinput.NewService(fake, 0, nil)
	defer svc.Stop()
	// Even a fully authenticated, allow-listed request is refused.
	code, body := callTalon(handleTalonRun(svc, remoteInputSeam{}, g), okReq, `{"command":"slide_next"}`)
	if code != http.StatusForbidden {
		t.Fatalf("default config must refuse run: %d %s", code, body)
	}
	if len(fake.RunActions) != 0 {
		t.Fatalf("run reached Talon: %v", fake.RunActions)
	}
}

func TestTalonUnsetTokenFailsClosed(t *testing.T) {
	svc := remoteinput.NewService(&remoteinput.FakeTalon{}, 0, nil)
	defer svc.Stop()
	g := talonGuard(t, true, true, []string{"slide_next"}, []string{"Keynote"}, "")
	for _, h := range []http.HandlerFunc{
		handleTalonRun(svc, remoteInputSeam{}, g), handleTalonInject(svc, remoteInputSeam{}, g),
	} {
		code, _ := callTalon(h, talonReq{remote: okReq.remote, auth: "Bearer "}, `{"command":"slide_next","text":"x","app":"Keynote"}`)
		if code != http.StatusServiceUnavailable {
			t.Fatalf("no token configured: got %d want 503", code)
		}
	}
}

func TestTalonInjectGate(t *testing.T) {
	fake := &remoteinput.FakeTalon{}
	svc := remoteinput.NewService(fake, 0, nil)
	defer svc.Stop()
	// Run enabled, inject NOT: the paths are independent gates.
	runOnly := talonGuard(t, true, false, nil, []string{"Keynote"}, talonTestToken)
	if code, _ := callTalon(handleTalonInject(svc, remoteInputSeam{}, runOnly), okReq,
		`{"text":"hi","app":"Keynote"}`); code != http.StatusForbidden {
		t.Fatalf("inject with only run enabled: got %d want 403", code)
	}
	// Inject enabled, run NOT.
	injOnly := talonGuard(t, false, true, []string{"slide_next"}, []string{"Keynote"}, talonTestToken)
	if code, _ := callTalon(handleTalonRun(svc, remoteInputSeam{}, injOnly), okReq,
		`{"command":"slide_next"}`); code != http.StatusForbidden {
		t.Fatalf("run with only inject enabled: got %d want 403", code)
	}

	h := handleTalonInject(svc, remoteInputSeam{}, injOnly)
	for name, c := range map[string]struct {
		rq   talonReq
		body string
		want int
	}{
		"app not listed":     {okReq, `{"text":"hi","app":"Terminal"}`, http.StatusForbidden},
		"no app (unfocused)": {okReq, `{"text":"hi"}`, http.StatusForbidden},
		"empty text":         {okReq, `{"text":"","app":"Keynote"}`, http.StatusBadRequest},
		"missing token":      {talonReq{remote: okReq.remote}, `{"text":"hi","app":"Keynote"}`, http.StatusUnauthorized},
		"wrong token":        {talonReq{remote: okReq.remote, auth: "Bearer x"}, `{"text":"hi","app":"Keynote"}`, http.StatusUnauthorized},
		"bad origin":         {talonReq{remote: "1.2.3.4:9", auth: okReq.auth}, `{"text":"hi","app":"Keynote"}`, http.StatusForbidden},
	} {
		if code, body := callTalon(h, c.rq, c.body); code != c.want {
			t.Errorf("%s: got %d want %d (%s)", name, code, c.want, body)
		}
	}
	if len(fake.Inserts) != 0 || len(fake.FocusAppCalls) != 0 {
		t.Fatalf("a refused injection reached Talon: %+v", fake)
	}

	code, body := callTalon(h, okReq, `{"text":"hello","app":"keynote"}`)
	if code != http.StatusAccepted {
		t.Fatalf("allowed inject: %d %s", code, body)
	}
}
