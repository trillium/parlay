package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"parlay/go-server/internal/store"
)

func demoMux(t *testing.T) (*http.ServeMux, *store.Store, *broker) {
	t.Helper()
	st := newTestStore(t)
	b := newBroker()
	mux := http.NewServeMux()
	registerDemo(mux, st, b)
	return mux, st, b
}

func demoDo(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// The audience view returns the demo channel and nothing else, even though
// the message store and the ledger are shared with every other channel.
func TestDemoViewIsScopedToTheDemoChannel(t *testing.T) {
	mux, st, b := demoMux(t)
	for _, m := range []store.ChatMessage{
		{Role: "user", Text: "private work message", Channel: "some-other-agent", From: "captain"},
		{Role: "agent", Text: "another agent's reply", Channel: "yet-another", From: "x"},
		{Role: "user", Text: "visible to audience", Channel: demoChannel, From: "browser-demo"},
		{Role: "agent", Text: "anonymous reply", Channel: demoChannel},
	} {
		stored, _, err := appendAndPublish(st, b, m)
		if err != nil {
			t.Fatal(err)
		}
		recordQueued(st, stored, inputSourceSend)
	}
	time.Sleep(100 * time.Millisecond) // ledger writer is async

	rec := demoDo(mux, http.MethodGet, demoViewPath, "")
	if rec.Code != 200 {
		t.Fatalf("view: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, leak := range []string{"private work message", "another agent's reply", "some-other-agent", "yet-another", "captain"} {
		if strings.Contains(body, leak) {
			t.Errorf("view leaked %q: %s", leak, body)
		}
	}
	var v demoViewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Label != "LIVE" || len(v.Messages) != 2 || v.Messages[0].Text != "visible to audience" {
		t.Fatalf("unexpected view: %+v", v)
	}
	if v.Messages[1].From != demoChannel {
		t.Errorf("agent line with no sender must be named after the channel, got %q", v.Messages[1].From)
	}
	if len(v.Hops) != 1 || v.Hops[0].InputID != v.Messages[0].ID {
		t.Fatalf("hops must join only on demo message ids: %+v", v.Hops)
	}
}

func TestDemoViewRedactsSecretsAndHomePaths(t *testing.T) {
	mux, st, b := demoMux(t)
	secret := "my GITHUB_TOKEN=ghp_abcdefghijklmnop1234 at /Users/someone/code and Bearer abcdefgh12345678"
	if _, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: secret, Channel: demoChannel}); err != nil {
		t.Fatal(err)
	}
	body := demoDo(mux, http.MethodGet, demoViewPath, "").Body.String()
	for _, leak := range []string{"ghp_abcdefghijklmnop1234", "someone", "abcdefgh12345678"} {
		if strings.Contains(body, leak) {
			t.Errorf("redaction missed %q in %s", leak, body)
		}
	}
}

func TestDemoTriggerRecordsSourceDestinationPayloadOutcome(t *testing.T) {
	t.Setenv(DemoResponderEnv, "")
	mux, _, _ := demoMux(t)
	rec := demoDo(mux, http.MethodPost, demoTriggerPath, `{"text":"ping"}`)
	if rec.Code != 200 {
		t.Fatalf("trigger: %d %s", rec.Code, rec.Body.String())
	}
	var ev demoEvent
	if err := json.Unmarshal(rec.Body.Bytes(), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Label != "LIVE" || ev.Payload != "ping" || !strings.Contains(ev.Destination, demoChannel) ||
		!strings.Contains(ev.Source, demoTriggerPath) || ev.Reply == nil || ev.Reply.From != demoResponder {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if len(ev.Hops) != 1 || ev.Hops[0].Stage != "queued" || ev.Hops[0].Class != "ok" || ev.Hops[0].Source != inputSourceDemo || ev.Hops[0].InputID != ev.ID {
		t.Fatalf("expected one queued/ok demo hop for %s, got %+v", ev.ID, ev.Hops)
	}
}

func TestDemoTriggerWithoutResponderWaitsForARealAgent(t *testing.T) {
	t.Setenv(DemoResponderEnv, "off")
	mux, _, _ := demoMux(t)
	var ev demoEvent
	_ = json.Unmarshal(demoDo(mux, http.MethodPost, demoTriggerPath, `{}`).Body.Bytes(), &ev)
	if ev.Reply != nil || !strings.Contains(ev.Outcome, "waiting for a real agent") || ev.Payload != demoDefaultText {
		t.Fatalf("unexpected: %+v", ev)
	}
}

func TestDemoTriggerResponderChoicePerRequest(t *testing.T) {
	t.Setenv(DemoResponderEnv, "")
	mux, _, _ := demoMux(t)
	var ev demoEvent
	_ = json.Unmarshal(demoDo(mux, http.MethodPost, demoTriggerPath, `{"responder":"agent"}`).Body.Bytes(), &ev)
	if ev.Reply != nil || !strings.Contains(ev.Outcome, "waiting for a real agent") {
		t.Errorf("agent choice must skip the built-in reply: %+v", ev)
	}
	t.Setenv(DemoResponderEnv, "off")
	ev = demoEvent{}
	_ = json.Unmarshal(demoDo(mux, http.MethodPost, demoTriggerPath, `{"responder":"builtin"}`).Body.Bytes(), &ev)
	if ev.Reply == nil {
		t.Errorf("builtin choice must answer even when env says off: %+v", ev)
	}
	if rec := demoDo(mux, http.MethodPost, demoTriggerPath, `{"responder":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad responder: %d", rec.Code)
	}
}

func TestDemoTriggerRejectsLongTextAndWrongMethod(t *testing.T) {
	mux, _, _ := demoMux(t)
	if rec := demoDo(mux, http.MethodPost, demoTriggerPath, `{"text":"`+strings.Repeat("a", demoMaxChars+1)+`"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("long text: %d", rec.Code)
	}
	if rec := demoDo(mux, http.MethodGet, demoTriggerPath, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET trigger: %d", rec.Code)
	}
}

func TestDemoResetClearsOnlyTheDemoChannel(t *testing.T) {
	mux, st, b := demoMux(t)
	for _, ch := range []string{demoChannel, "keep-me"} {
		if _, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: "x", Channel: ch}); err != nil {
			t.Fatal(err)
		}
	}
	if rec := demoDo(mux, http.MethodPost, demoResetPath, "{}"); rec.Code != 200 {
		t.Fatalf("reset: %d", rec.Code)
	}
	if got := st.Messages.History(0); len(got) != 1 || got[0].Channel != "keep-me" {
		t.Fatalf("reset touched other channels: %+v", got)
	}
}

// The RECORDED path must be self-contained, self-consistent, and free of
// anything redaction would change (a fixture with a secret-shaped string or a
// home path would be the leak this demo exists to avoid).
func TestDemoPageInlinesAValidRecordedFixture(t *testing.T) {
	mux, _, _ := demoMux(t)
	rec := demoDo(mux, http.MethodGet, "/demo", "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), demoRecordingMarker) {
		t.Fatalf("page: %d, marker left in body=%v", rec.Code, strings.Contains(rec.Body.String(), demoRecordingMarker))
	}
	for _, want := range []string{`id="badge"`, "RECORDED", "LIVE", `role="log"`, "/api/chat/demo/view"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("page missing %q", want)
		}
	}

	var fx struct {
		Label  string `json:"label"`
		Frames []struct {
			T    int    `json:"t"`
			Text string `json:"text"`
		} `json:"frames"`
		Event demoEvent `json:"event"`
	}
	if err := json.Unmarshal(demoRecording, &fx); err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}
	if fx.Label != "RECORDED" || fx.Event.Label != "RECORDED" || len(fx.Frames) == 0 {
		t.Fatalf("fixture must be labelled RECORDED and non-empty: %+v", fx)
	}
	if redactDemo(string(demoRecording)) != string(demoRecording) {
		t.Error("fixture contains something the redactor would mask (secret-shaped string or home path)")
	}
	// The reply's quoted character count must match the payload it describes.
	want := "received " + itoaDemo(utf8.RuneCountInString(fx.Event.Payload)) + " characters"
	if !strings.Contains(fx.Event.Reply.Text, want) {
		t.Errorf("fixture reply %q does not state %q", fx.Event.Reply.Text, want)
	}
}

// The inspector must follow the event: with the responder off, a later agent
// reply and its delivered hop have to be visible in the same /view the page
// polls, keyed by the event id, and the page must re-render from each poll.
func TestDemoViewCarriesLaterRepliesAndHopsForTheInspector(t *testing.T) {
	t.Setenv(DemoResponderEnv, "off")
	mux, st, b := demoMux(t)
	var ev demoEvent
	_ = json.Unmarshal(demoDo(mux, http.MethodPost, demoTriggerPath, `{}`).Body.Bytes(), &ev)
	sent := st.Messages.History(0)[0]
	recordDelivered(st, sent, inputSourcePollWake)
	if _, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "agent", Text: "real answer", Channel: demoChannel}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // ledger writer is async
	var v demoViewResponse
	_ = json.Unmarshal(demoDo(mux, http.MethodGet, demoViewPath, "").Body.Bytes(), &v)
	stages := map[string]bool{}
	for _, h := range v.Hops {
		if h.InputID == ev.ID {
			stages[h.Stage] = true
		}
	}
	if !stages["queued"] || !stages["delivered"] {
		t.Errorf("view must carry queued and delivered hops for %s: %+v", ev.ID, v.Hops)
	}
	last := v.Messages[len(v.Messages)-1]
	if last.Role != "agent" || last.From != demoChannel || last.Text != "real answer" {
		t.Errorf("unexpected reply line: %+v", last)
	}
}

func itoaDemo(n int) string { b, _ := json.Marshal(n); return string(b) }

// The page script is the whole demo; a syntax error kills mode switching,
// polling and the recorded replay. Parse the script exactly as served.
func TestDemoPageInlineScriptParses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	mux, _, _ := demoMux(t)
	rec := demoDo(mux, http.MethodGet, "/demo", "")
	m := regexp.MustCompile(`(?s)<script>(.*)</script>`).FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatal("served page has no inline script")
	}
	file := filepath.Join(t.TempDir(), "demo.js")
	if err := os.WriteFile(file, []byte(m[1]), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", file).CombinedOutput(); err != nil {
		t.Fatalf("inline script does not parse: %v\n%s", err, out)
	}
}

const demoPageHarness = `
const vm = require("vm"), fs = require("fs");
const script = fs.readFileSync(process.argv[2], "utf8");
class El {
  constructor(id) { this.id = id; this.kids = []; this.own = ""; this.dataset = {}; this.attrs = {}; this.scrollTop = 0; this.clientHeight = 0; this.scrollHeight = 0; }
  get textContent() { return this.own + this.kids.map(k => k.textContent).join(""); }
  set textContent(v) { this.kids = []; this.own = String(v); }
  appendChild(k) { this.kids.push(k); return k; }
  setAttribute(k, v) { this.attrs[k] = v; }
}
const els = {};
const get = id => els[id] || (els[id] = new El(id));
get("recording").textContent = JSON.stringify({ frames: [{ t: 0, text: "x", role: "agent" }], event: { label: "RECORDED", source: "s", destination: "d", payload: "p", outcome: "o", hops: [], reply: null } });
let interval = null, view = null, failing = false;
const msg = (id, role, text, from) => ({ id, role, text, from, ts: "2026-10-20T10:00:00Z" });
const sandbox = {
  document: { getElementById: get, createElement: () => new El(), createTextNode: t => ({ textContent: t }), body: new El("body"), title: "", addEventListener() {} },
  location: { search: "" }, URLSearchParams,
  setInterval: f => { interval = f; return 1; }, clearInterval() {}, setTimeout: () => 1, clearTimeout() {},
  fetch: (url, opts) => {
    if (url.endsWith("/trigger")) return Promise.resolve({ ok: true, json: async () => ({ id: "e1", label: "LIVE", source: "s", destination: "d", payload: "p", outcome: "queued", hops: [], reply: null }) });
    if (failing) return Promise.reject(new Error("down"));
    return Promise.resolve({ ok: true, json: async () => view });
  },
};
const tick = () => new Promise(r => setImmediate(r));
const snap = () => ({ badge: get("badge").textContent, terminal: get("terminal").textContent, outcome: get("e-outcome").textContent });
(async () => {
  view = { channel: "talk-demo", messages: [], hops: [] };
  vm.runInNewContext(script, sandbox);
  await tick();
  const out = { start: snap() };
  view = { channel: "talk-demo", messages: [msg("e1", "user", "hello", "me")], hops: [] };
  get("fire").onclick(); await tick(); await tick();
  out.queued = snap();
  failing = true;
  interval(); await tick();
  out.oneFail = snap();
  interval(); await tick();
  out.twoFail = snap();
  failing = false;
  view = { channel: "talk-demo", messages: [msg("e1", "user", "hello", "me"), msg("a1", "agent", "real answer", "talk-demo")], hops: [{ inputId: "e1", seq: 1, stage: "delivered", class: "c", source: "x" }] };
  interval(); await tick();
  out.recovered = snap();
  out.hopRows = get("hops").kids.length;
  console.log(JSON.stringify(out));
})();
`

// Runs the served page script under node with a stubbed DOM and fetch and
// checks what the audience would see: B2 disconnect/recovery, B1 follow-up.
func TestDemoPageBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	mux, _, _ := demoMux(t)
	m := regexp.MustCompile(`(?s)<script>(.*)</script>`).FindStringSubmatch(demoDo(mux, http.MethodGet, "/demo", "").Body.String())
	if m == nil {
		t.Fatal("served page has no inline script")
	}
	dir := t.TempDir()
	page, harness := filepath.Join(dir, "page.js"), filepath.Join(dir, "harness.js")
	if err := os.WriteFile(page, []byte(m[1]), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harness, []byte(demoPageHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(node, harness, page).CombinedOutput()
	if err != nil {
		t.Fatalf("harness failed: %v\n%s", err, raw)
	}
	type snap struct{ Badge, Terminal, Outcome string }
	var got struct {
		Start, Queued, OneFail, TwoFail, Recovered snap
		HopRows                                    int
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("harness output %q: %v", raw, err)
	}
	if got.Start.Badge != "LIVE" || got.OneFail.Badge != "LIVE" {
		t.Errorf("badge must stay LIVE until 2 failed polls: %+v %+v", got.Start, got.OneFail)
	}
	if got.TwoFail.Badge != "LIVE - DISCONNECTED" || !strings.HasPrefix(got.TwoFail.Terminal, "STALE since ") {
		t.Errorf("after 2 failed polls: %+v", got.TwoFail)
	}
	if got.Recovered.Badge != "LIVE" || strings.Contains(got.Recovered.Terminal, "STALE since") {
		t.Errorf("recovery must restore LIVE: %+v", got.Recovered)
	}
	if strings.Contains(got.Queued.Outcome, "answered by") || !strings.Contains(got.Recovered.Outcome, "answered by talk-demo") || !strings.Contains(got.Recovered.Outcome, "real answer") {
		t.Errorf("outcome must follow the later agent reply: queued=%q recovered=%q", got.Queued.Outcome, got.Recovered.Outcome)
	}
	if got.HopRows != 1 {
		t.Errorf("hop table must re-render from the poll: %d rows", got.HopRows)
	}
}
