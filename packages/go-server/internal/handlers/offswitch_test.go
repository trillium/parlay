package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"parlay/go-server/internal/store"
)

// A muted CONNECTION must be refused before the engine is consulted at all —
// otherwise "off" would only be hiding the entry, not revoking the surface.
func TestOffConnectionRefusesEvalWithoutCallingTheEngine(t *testing.T) {
	st := newTestStore(t)
	hub := newHub(newBroker())
	calls := 0
	fakeEngine(t, func(w http.ResponseWriter, r *http.Request) { calls++; okEngine("clear", "clear")(w, r) })

	if _, _, err := st.OffSwitch.Set(store.OffKindConnection, "dev-1", true, "tester", "cli"); err != nil {
		t.Fatal(err)
	}

	rec := evalWithStore(t, st, hub, `{"device":"dev-1","streamId":"eval-dev-1-main","version":1,"text":"x","voiceEnabled":true}`)

	if calls != 0 {
		t.Errorf("engine was called %d times for a muted connection; the refusal must precede the relay", calls)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["refused"] != store.OffKindConnection {
		t.Errorf("response refused = %v, want %q", resp["refused"], store.OffKindConnection)
	}
	rec2 := onlyRecord(t, st)
	if rec2.Outcome != store.OutcomeRefused || rec2.Reason != "off-connection" {
		t.Errorf("log record = %s/%s, want refused/off-connection", rec2.Outcome, rec2.Reason)
	}
	if rec2.Device != "dev-1" {
		t.Errorf("log record device = %q, want dev-1", rec2.Device)
	}
}

// The off-set must be readable back under ONE key from every route that
// publishes it. A flip that appears on the POST response but not on the two
// reads is exactly the failure this asserts against: it makes `parlay off
// status` report "nothing is off" over a target that is genuinely off, which is
// a lie about whether the kill switch is engaged. Caught live during the
// end-to-end demo, not by the store tests, because each route looked correct on
// its own.
func TestEveryRoutePublishesTheOffSetUnderTheSameKey(t *testing.T) {
	st := newTestStore(t)
	post := handleOffSwitch(st)

	rec := httptest.NewRecorder()
	post(rec, httptest.NewRequest(http.MethodPost, "/api/chat/off-switch", strings.NewReader(
		`{"kind":"action","id":"clear","off":true,"by":"t","surface":"cli"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d", rec.Code)
	}

	getOff := httptest.NewRecorder()
	post(getOff, httptest.NewRequest(http.MethodGet, "/api/chat/off-switch", nil))
	var offDoc map[string]json.RawMessage
	if err := json.Unmarshal(getOff.Body.Bytes(), &offDoc); err != nil {
		t.Fatalf("decode GET off-switch: %v", err)
	}

	logRec := httptest.NewRecorder()
	handleActionLog(st)(logRec, httptest.NewRequest(http.MethodGet, "/api/chat/action-log", nil))
	var logDoc map[string]json.RawMessage
	if err := json.Unmarshal(logRec.Body.Bytes(), &logDoc); err != nil {
		t.Fatalf("decode GET action-log: %v", err)
	}

	for name, doc := range map[string]map[string]json.RawMessage{"off-switch": offDoc, "action-log": logDoc} {
		raw, ok := doc["targets"]
		if !ok {
			t.Fatalf("%s publishes no \"targets\" key; keys are %v", name, keysOf(doc))
		}
		var targets []store.OffEntry
		if err := json.Unmarshal(raw, &targets); err != nil {
			t.Fatalf("%s targets: %v", name, err)
		}
		if len(targets) != 1 || targets[0].Kind != "action" || targets[0].ID != "clear" {
			t.Errorf("%s targets = %+v, want the single action clear", name, targets)
		}
		if _, stale := doc["off"]; stale {
			t.Errorf("%s still carries the old \"off\" key for the target list", name)
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// A muted ACTION must have its emission suppressed — not delivered with the
// symptom hidden.
func TestOffActionSuppressesEmission(t *testing.T) {
	st := newTestStore(t)
	hub := newHub(newBroker())
	fakeEngine(t, okEngine("clear", "clear"))
	if _, _, err := st.OffSwitch.Set(store.OffKindAction, "clear", true, "tester", "api"); err != nil {
		t.Fatal(err)
	}

	rec := evalWithStore(t, st, hub, `{"device":"dev-1","streamId":"eval-dev-1-main","version":1,"text":"change inside input","voiceEnabled":true}`)

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["refused"] != store.OffKindAction {
		t.Errorf("response refused = %v, want %q", resp["refused"], store.OffKindAction)
	}
	if resp["action"] != "clear" {
		t.Errorf("response action = %v, want clear", resp["action"])
	}
	if _, ok := resp["actions"]; ok {
		t.Error("a muted action still returned an actions payload; the emission was not suppressed")
	}
	got := onlyRecord(t, st)
	if got.Outcome != store.OutcomeRefused || got.Reason != "off-action" || got.InputAction != "clear" {
		t.Errorf("log record = %+v, want refused/off-action with inputAction clear", got)
	}
}

// The off switch is reachable and reversible over HTTP, and every validation
// failure refuses without mutating.
func TestOffSwitchRouteLifecycle(t *testing.T) {
	st := newTestStore(t)
	h := handleOffSwitch(st)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/chat/off-switch", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}
	get := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/chat/off-switch", nil)
		rec := httptest.NewRecorder()
		h(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode GET: %v", err)
		}
		return out
	}

	for _, bad := range []string{
		`{"kind":"connection","id":"d1"}`,          // no `off`
		`{"kind":"nonsense","id":"d1","off":true}`, // unknown kind
		`{"kind":"connection","id":"","off":true}`, // no id
	} {
		if rec := post(bad); rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s: status = %d, want 400", bad, rec.Code)
		}
	}
	if !st.OffSwitch.IsEmpty() {
		t.Fatal("a refused POST still wrote to the off switch")
	}

	if rec := post(`{"kind":"connection","id":"dev-9","off":true,"by":"tester","surface":"cli"}`); rec.Code != http.StatusOK {
		t.Fatalf("valid POST: status = %d, body %s", rec.Code, rec.Body.String())
	}
	if !st.OffSwitch.IsConnectionOff("dev-9") {
		t.Fatal("POST off:true did not turn the connection off")
	}
	state := get()
	if state["connections"] != float64(1) {
		t.Errorf("GET connections = %v, want 1", state["connections"])
	}

	if rec := post(`{"kind":"connection","id":"dev-9","off":false,"surface":"cli"}`); rec.Code != http.StatusOK {
		t.Fatalf("POST off:false: status = %d", rec.Code)
	}
	if st.OffSwitch.IsConnectionOff("dev-9") {
		t.Fatal("POST off:false did not turn the connection back on")
	}
	if !st.OffSwitch.IsEmpty() {
		t.Fatal("turning the last target back on left entries behind")
	}
}

// A muted SUBMIT must not fire through the back door. The engine has already
// evaluated the text and armed its own timer by the time the relay refuses the
// action, so the refusal has to leave the stream's fired command recorded —
// otherwise /eval-push looks up the stream's PREVIOUS command, its own mute check
// misses, and the deferred fire is broadcast. That ordering bug is what this
// pins: rememberFired must run before the mute check returns.
func TestMutedSubmitIsRefusedWhenItsOwnTimerFires(t *testing.T) {
	st := newTestStore(t)
	hub := newHub(newBroker())
	// The engine arms the submit timer, naming `submit` as the fired command.
	fakeEngine(t, okEngine("submit", "armTimer"))
	if _, _, err := st.OffSwitch.Set(store.OffKindAction, "submit", true, "tester", "cli"); err != nil {
		t.Fatal(err)
	}

	rec := evalWithStore(t, st, hub,
		`{"device":"dev-x","streamId":"eval-dev-x-main","version":1,"text":"send it","voiceEnabled":true}`)
	var evalResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &evalResp); err != nil {
		t.Fatalf("decode eval response: %v", err)
	}
	if evalResp["refused"] != store.OffKindAction {
		t.Fatalf("eval was not refused for the muted action: %v", evalResp)
	}

	// Now the engine's timer elapses and it pushes the fire it armed.
	pushReq := httptest.NewRequest(http.MethodPost, "/api/chat/eval-push", strings.NewReader(
		`{"streamId":"eval-dev-x-main","seq":2,"baseVersion":1,"v":1,"action":{"verb":"submitNow"}}`))
	pushReq.Header.Set("Content-Type", "application/json")
	pushRec := httptest.NewRecorder()
	handleEvalPush(st, hub)(pushRec, pushReq)

	var pushResp map[string]any
	if err := json.Unmarshal(pushRec.Body.Bytes(), &pushResp); err != nil {
		t.Fatalf("decode push response: %v", err)
	}
	if pushResp["ok"] != false || pushResp["refused"] != store.OffKindAction {
		t.Errorf("a muted submit's own timer still fired: %v", pushResp)
	}

	// And the refusal is readable in the log, twice: the evaluation and the fire.
	var refused []store.ActionRecord
	for _, r := range st.ActionLog.List(store.ActionLogFilter{Outcome: store.OutcomeRefused}) {
		if r.Reason == "off-action" {
			refused = append(refused, r)
		}
	}
	if len(refused) != 2 {
		t.Errorf("refused/off-action rows = %d, want 2 (the evaluation and the refused fire): %+v", len(refused), refused)
	}
}
