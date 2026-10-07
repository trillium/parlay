package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// actionLogServer stands up a fake server speaking the two new routes and
// records what the CLI asked for, so a test can assert on the REQUEST (the
// filters, the flip) and not merely on the printout.
type actionLogServer struct {
	t       *testing.T
	queries []string
	posts   []map[string]any
}

func newActionLogServer(t *testing.T, resp actionLogResponse, postResp map[string]any) (*actionLogServer, *httptest.Server) {
	return newActionLogServerStatus(t, resp, postResp, http.StatusOK)
}

// newActionLogServerStatus is newActionLogServer with control over the log
// route's status, so the two failure classes can each be exercised: a 404 means
// "older server" and must read as one, while any other non-2xx is a REAL error
// that must exit non-zero.
func newActionLogServerStatus(t *testing.T, resp actionLogResponse, postResp map[string]any, logStatus int) (*actionLogServer, *httptest.Server) {
	t.Helper()
	rec := &actionLogServer{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/action-log", func(w http.ResponseWriter, r *http.Request) {
		rec.queries = append(rec.queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		if logStatus != http.StatusOK {
			w.WriteHeader(logStatus)
			json.NewEncoder(w).Encode(map[string]string{"error": "since: want an RFC3339 timestamp or a duration like 15m"})
			return
		}
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/api/chat/off-switch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "targets": resp.Targets})
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		rec.posts = append(rec.posts, body)
		out := map[string]any{}
		for k, v := range postResp {
			out[k] = v
		}
		out["kind"] = body["kind"]
		out["id"] = body["id"]
		out["off"] = body["off"]
		json.NewEncoder(w).Encode(out)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("PARLAY_SERVER", srv.URL)
	return rec, srv
}

func sampleLog() actionLogResponse {
	return actionLogResponse{
		OK: true, Total: 2, Limit: 200,
		Records: []actionRecord{
			{ID: "act-2", At: "2026-10-07T17:09:17.101109Z", Source: "test-site", Device: "d1",
				InputAction: "clear", OutputActions: []string{"clear"}, Outcome: "dropped", Reason: "preview-suppressed"},
			{ID: "act-1", At: "2026-10-07T17:09:16.000000Z", Source: "panel", Device: "d1",
				InputAction: "submit", OutputActions: []string{"armTimer"}, Outcome: "queued", Reason: "submit-armed"},
		},
		Facets:            actionLogFacets{InputActions: []string{"clear", "submit"}, Outcomes: []string{"dropped", "queued"}},
		OutcomeVocabulary: []string{"delivered", "queued", "dropped", "refused"},
	}
}

// The printout must name all four outcomes even when no record has produced
// one, so a reader never mistakes "no refused rows" for "refused is not a thing
// this log can show".
func TestActionLogPrintsTheWholeOutcomeVocabulary(t *testing.T) {
	srv := &actionLogServer{}
	_ = srv
	resp := sampleLog()
	_, _ = newActionLogServer(t, resp, nil)

	out := captureStdout(t, func() { ActionLog(nil) })

	for _, want := range []string{"delivered", "queued", "dropped", "refused"} {
		if !strings.Contains(out, want) {
			t.Errorf("printout omits outcome %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "preview-suppressed") || !strings.Contains(out, "submit-armed") {
		t.Errorf("printout omits the reason tokens that make an outcome actionable:\n%s", out)
	}
	if !strings.Contains(out, "inputAction") || !strings.Contains(out, "outputAction") {
		t.Errorf("printout does not name the filter axes:\n%s", out)
	}
	if !strings.Contains(out, "off: nothing") {
		t.Errorf("printout has no off-switch line:\n%s", out)
	}
}

// Every filter flag must reach the server as a query parameter: a filter the
// CLI accepts and then drops is worse than one it refuses.
func TestActionLogSendsEveryFilterToTheServer(t *testing.T) {
	rec, _ := newActionLogServer(t, sampleLog(), nil)

	captureStdout(t, func() {
		ActionLog([]string{
			"--source", "test-site", "--input-action", "clear", "--output-action", "clear",
			"--outcome", "dropped", "--reason", "preview-suppressed", "--device", "d1",
			"--since", "15m", "--until", "1m", "--limit", "5",
		})
	})

	if len(rec.queries) != 1 {
		t.Fatalf("CLI made %d reads, want 1", len(rec.queries))
	}
	q := rec.queries[0]
	for _, want := range []string{
		"source=test-site", "inputAction=clear", "outputAction=clear", "outcome=dropped",
		"reason=preview-suppressed", "device=d1", "since=15m", "until=1m", "limit=5",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q is missing %q", q, want)
		}
	}
}

// `--off` is the CLI half of "reachable from where the logs are read": the
// same invocation that read the rows flips the target.
func TestActionLogOffFlagFlipsThroughTheOffSwitchRoute(t *testing.T) {
	rec, _ := newActionLogServer(t, sampleLog(), map[string]any{"ok": true, "changed": true})

	captureStdout(t, func() { ActionLog([]string{"--off", "action:clear"}) })

	if len(rec.posts) != 1 {
		t.Fatalf("--off made %d flips, want 1", len(rec.posts))
	}
	got := rec.posts[0]
	if got["kind"] != "action" || got["id"] != "clear" || got["off"] != true || got["surface"] != "cli" {
		t.Errorf("flip body = %v, want action/clear/true/cli", got)
	}
}

// The direct verbs are the same operation, and `on` is its exact inverse.
func TestOffAndOnVerbsAreInverses(t *testing.T) {
	rec, _ := newActionLogServer(t, sampleLog(), map[string]any{"ok": true, "changed": true})

	captureStdout(t, func() { OffSwitch([]string{"connection", "dev-1"}, true, "off") })
	captureStdout(t, func() { OffSwitch([]string{"connection", "dev-1"}, false, "on") })

	if len(rec.posts) != 2 {
		t.Fatalf("made %d flips, want 2", len(rec.posts))
	}
	if rec.posts[0]["off"] != true || rec.posts[1]["off"] != false {
		t.Errorf("flip bodies = %v, want off:true then off:false", rec.posts)
	}
	for i, p := range rec.posts {
		if p["kind"] != "connection" || p["id"] != "dev-1" {
			t.Errorf("flip %d = %v, want connection/dev-1", i, p)
		}
	}
}

// An unknown kind is a usage error, and it must not reach the server.
func TestOffVerbRefusesAnUnknownKind(t *testing.T) {
	rec, _ := newActionLogServer(t, sampleLog(), map[string]any{"ok": true})

	code, exited := withExitTrap(t, func() { OffSwitch([]string{"widget", "x"}, true, "off") })

	if !exited || code != config.ExitUsage {
		t.Errorf("exit = (%d, %v), want usage exit 2", code, exited)
	}
	if len(rec.posts) != 0 {
		t.Errorf("a refused kind still hit the server: %v", rec.posts)
	}
}

// `off status` (and a bare `off`) read the state rather than doing nothing.
func TestOffStatusReadsTheTargets(t *testing.T) {
	resp := sampleLog()
	resp.Targets = []offEntry{{Kind: "action", ID: "clear", Surface: "website"}}
	_, _ = newActionLogServer(t, resp, nil)

	out := captureStdout(t, func() { OffSwitch(nil, true, "off") })

	if !strings.Contains(out, "action clear") || !strings.Contains(out, "website") {
		t.Errorf("status output does not name the off target and its surface:\n%s", out)
	}
}

// The JSON form must decode the same record shape the server sends, including
// every field — a CLI that silently drops a field the page can see is the
// drift this test exists to catch.
func TestActionLogJSONRoundTrip(t *testing.T) {
	rec, _ := newActionLogServer(t, sampleLog(), nil)

	out := captureStdout(t, func() { ActionLog([]string{"--json"}) })
	if len(rec.queries) != 1 {
		t.Fatalf("--json made %d reads, want 1", len(rec.queries))
	}
	var decoded actionLogResponse
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("--json is not decodable: %v\n%s", err, out)
	}
	if len(decoded.Records) != 2 || decoded.Records[0].Reason != "preview-suppressed" {
		t.Errorf("JSON round trip lost a field: %+v", decoded.Records)
	}
	if decoded.Records[0].RelayMs != 0 || decoded.Records[0].EngineEvalNs != 0 {
		t.Errorf("unexpected timings: %+v", decoded.Records[0])
	}
}

// --json decodes and re-encodes the server's payload, so a field the server sends
// and the CLI's type does not carry is silently dropped. filterVocabulary is the
// one field that was missing, and the CLI's own help promises "the same field
// names the API sends".
func TestActionLogJSONKeepsTheServersFilterVocabulary(t *testing.T) {
	resp := sampleLog()
	resp.FilterVocabulary = json.RawMessage(`{"fields":["source","inputAction"],"times":["since","until"]}`)
	_, _ = newActionLogServer(t, resp, nil)

	out := captureStdout(t, func() { ActionLog([]string{"--json"}) })

	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("--json is not one JSON document: %v\n%s", err, out)
	}
	if _, ok := decoded["filterVocabulary"]; !ok {
		t.Errorf("--json dropped the server's filterVocabulary:\n%s", out)
	}
}

// --status reports the off-set; --off changes it. Doing only the first would
// leave the requested flip undone behind a success exit.
func TestActionLogRefusesStatusTogetherWithOff(t *testing.T) {
	rec, _ := newActionLogServer(t, sampleLog(), map[string]any{"ok": true})

	code, exited := withExitTrap(t, func() { ActionLog([]string{"--status", "--off", "action:clear"}) })

	if !exited || code != config.ExitUsage {
		t.Errorf("exit = (%d, %v), want usage exit 2", code, exited)
	}
	if len(rec.posts) != 0 {
		t.Errorf("a refused combination still flipped a target: %v", rec.posts)
	}
}

// --json --off must still flip, and must still leave stdout as ONE JSON document:
// a text confirmation printed in front of the JSON makes the output undecodable
// even though the change already landed.
func TestActionLogJSONOffKeepsStdoutDecodable(t *testing.T) {
	rec, _ := newActionLogServer(t, sampleLog(), map[string]any{"ok": true, "changed": true, "targets": []any{}})

	out := captureStdout(t, func() { ActionLog([]string{"--json", "--off", "action:clear"}) })

	if len(rec.posts) != 1 {
		t.Fatalf("--json --off made %d flips, want 1", len(rec.posts))
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v\n%s", err, out)
	}
	if _, ok := decoded["records"]; !ok {
		t.Errorf("stdout decoded but is not the log payload:\n%s", out)
	}
}

// A real server error (a mistyped --since is an HTTP 400) must NOT be reported as
// an older server, and must exit non-zero — otherwise a typo looks like an
// install problem behind a green exit code.
func TestActionLogSurfacesAServerErrorRatherThanCallingItUnsupported(t *testing.T) {
	_, _ = newActionLogServerStatus(t, sampleLog(), nil, http.StatusBadRequest)

	var code int
	var exited bool
	var out string
	// httpc.Die writes the refusal to stderr, so both descriptors are watched:
	// stdout for what was printed, stderr for why it refused.
	err := captureStderr(t, func() {
		out = captureStdout(t, func() {
			code, exited = withExitTrap(t, func() { ActionLog([]string{"--since", "not-a-time"}) })
		})
	})

	if !exited || code != config.ExitRuntime {
		t.Errorf("exit = (%d, %v), want runtime exit 1", code, exited)
	}
	if strings.Contains(out, "has no command log") {
		t.Errorf("an HTTP 400 was reported as an older server on stdout:\n%s", out)
	}
	if !strings.Contains(err, "400") {
		t.Errorf("the server's status did not reach the user on stderr:\n%s", err)
	}
}

// A genuinely missing route is the older-server case, and stays a success with a
// plain explanation.
func TestActionLogTreatsAMissingRouteAsAnOlderServer(t *testing.T) {
	_, _ = newActionLogServerStatus(t, sampleLog(), nil, http.StatusNotFound)

	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { ActionLog(nil) })
	})

	if exited && code != 0 {
		t.Errorf("a missing route exited %d, want success", code)
	}
	if !strings.Contains(out, "has no command log") {
		t.Errorf("a missing route was not explained as an older server:\n%s", out)
	}
}

// The connection column is how a reader gets a device id to feed the documented
// `parlay off connection <id>` control without switching to --json.
func TestActionLogRowShowsTheConnection(t *testing.T) {
	row := actionRow(actionRecord{
		At: "2026-10-07T17:00:00Z", Source: "test-site", Device: "dev-42",
		InputAction: "clear", OutputActions: []string{"clear"}, Outcome: "delivered",
	})
	if !strings.Contains(row, "dev-42") {
		t.Errorf("row omits the connection id:\n%s", row)
	}
	// A record with no connection must not print a misleading one.
	blank := actionRow(actionRecord{At: "2026-10-07T17:00:00Z", Source: "panel", Outcome: "dropped", Reason: "no-match"})
	if strings.Contains(blank, "dev") {
		t.Errorf("row invented a connection:\n%s", blank)
	}
}
