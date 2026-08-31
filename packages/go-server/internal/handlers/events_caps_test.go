package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"parlay/go-server/internal/capability"
	"parlay/go-server/internal/store"
)

// runEventsTarget is runEvents with a caller-chosen request target, for the
// ?caps= tests.
func runEventsTarget(t *testing.T, st *store.Store, hub *Hub, target string) (rec *httptest.ResponseRecorder, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx)
	rec = httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handleEvents(st, hub)(rec, req)
		close(done)
	}()
	return rec, func() {
		cancel()
		<-done
	}
}

// capsTarget url-encodes a raw declaration into an /api/chat/events target.
func capsTarget(raw string) string {
	return "/api/chat/events?caps=" + url.QueryEscape(raw)
}

// mustDeclaration parses a declaration literal for direct hub-level tests.
func mustDeclaration(t *testing.T, raw string) *capability.Declaration {
	t.Helper()
	d, err := capability.ParseDeclaration([]byte(raw))
	if err != nil {
		t.Fatalf("fixture declaration invalid: %v", err)
	}
	return d
}

func TestHandleEventsInvalidCapsRefusesConnection(t *testing.T) {
	// Invalid is a refusal, never a fallback to ungated legacy delivery
	// (docs/interface-capabilities.md): HTTP 400, JSON {error}, no stream.
	for name, raw := range map[string]string{
		"not json":          `{"schema":`,
		"empty":             ``,
		"wrong major":       `{"schema": "2.0.0", "surface": {"kind": "panel"}}`,
		"prerelease schema": `{"schema": "1.0.0-rc1", "surface": {"kind": "panel"}}`,
		"missing kind":      `{"schema": "1.0.0", "surface": {}}`,
	} {
		req := httptest.NewRequest(http.MethodGet, capsTarget(raw), nil)
		rec := httptest.NewRecorder()
		handleEvents(newTestStore(t), newHub(newBroker()))(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
			continue
		}
		var got map[string]string
		decodeBody(t, rec, &got)
		if got["error"] == "" {
			t.Errorf("%s: body = %q, want a JSON {error}", name, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "event: connected") {
			t.Errorf("%s: refused connection still opened a stream", name)
		}
	}
}

func TestHandleEventsDeclaringClientGetsCapabilitiesEcho(t *testing.T) {
	raw := `{"schema": "1.0.0", "surface": {"kind": "panel"}, "accepts": {"navigate": {}, "teleport": {}}}`
	rec, stop := runEventsTarget(t, newTestStore(t), newHub(newBroker()), capsTarget(raw))
	time.Sleep(50 * time.Millisecond) // let the initial burst land before we stop the stream
	stop()

	body := rec.Body.String()
	// The negotiation echo: which accepts names this server gates on vs.
	// never heard of, so the surface can see its inert names.
	want := `"capabilities":{"schema":"1.0.0","recognized":["navigate"],"unknown":["teleport"]}`
	if !strings.Contains(body, want) {
		t.Errorf("connected frame missing the capabilities echo %s; got:\n%s", want, body)
	}
}

func TestHandleEventsLegacyConnectedFrameUnchanged(t *testing.T) {
	// No ?caps= at all is the grandfathered legacy client: the connected
	// frame must stay byte-identical to the pre-capability behaviour.
	rec, stop := runEvents(t, newTestStore(t), newHub(newBroker()))
	time.Sleep(50 * time.Millisecond)
	stop()

	if !strings.Contains(rec.Body.String(), "event: connected\ndata: {}\n\n") {
		t.Errorf("legacy connected frame changed; got:\n%s", rec.Body.String())
	}
}

func TestBroadcastGatesPresentationCommands(t *testing.T) {
	hub := newHub(newBroker())
	declared, cancelD, err := hub.subscribeDeclared("", mustDeclaration(t,
		`{"schema": "1.0.0", "surface": {"kind": "panel"}, "accepts": {"navigate": {}}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer cancelD()
	legacy, cancelL := hub.subscribe("")
	defer cancelL()

	// The gate table, at the live choke point: accepted command → both;
	// unaccepted command → legacy only; state report → both.
	hub.broadcast("navigate", map[string]string{"url": "/x"})
	awaitEvent(t, declared, "navigate", time.Second)
	awaitEvent(t, legacy, "navigate", time.Second)

	hub.broadcast("reload", struct{}{})
	hub.broadcast(eventMessage, store.ChatMessage{ID: "m1"})
	awaitEvent(t, legacy, "reload", time.Second)
	// broadcast enqueues synchronously, so if reload had been delivered it
	// would sit in the declared channel AHEAD of message — the next event
	// being message proves the suppression, with no skip that could mask it.
	select {
	case ev := <-declared:
		if ev.name != eventMessage {
			t.Fatalf("declared client's next event = %q, want %q (reload must be suppressed)", ev.name, eventMessage)
		}
	case <-time.After(time.Second):
		t.Fatal("declared client did not receive the ungated message event")
	}

	if got := hub.capabilitySuppressed(); got["reload"] != 1 || len(got) != 1 {
		t.Errorf("capabilitySuppressed = %v, want map[reload:1]", got)
	}
}

func TestBroadcastToDeviceCountExcludesSuppressed(t *testing.T) {
	hub := newHub(newBroker())
	_, cancel, err := hub.subscribeDeclared("dev-1", mustDeclaration(t,
		`{"schema": "1.0.0", "surface": {"kind": "panel", "instance": "dev-1"}, "accepts": {"navigate": {}}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	// Delivery truth, not addressing truth: a matched-but-suppressed client
	// does not count (mirrors the TS side's broadcastToDevice).
	if got := hub.broadcastToDevice("dev-1", "reload", struct{}{}); got != 0 {
		t.Errorf("broadcastToDevice(reload) = %d, want 0 (suppressed)", got)
	}
	if got := hub.broadcastToDevice("dev-1", "navigate", struct{}{}); got != 1 {
		t.Errorf("broadcastToDevice(navigate) = %d, want 1 (accepted)", got)
	}
	if got := hub.capabilitySuppressed(); got["reload"] != 1 {
		t.Errorf("capabilitySuppressed = %v, want reload counted once", got)
	}
}

func TestHandleSubscribersReportsCapabilityFields(t *testing.T) {
	st := newTestStore(t)
	hub := newHub(newBroker())
	_, cancel, err := hub.subscribeDeclared("dev-9", mustDeclaration(t,
		`{"schema": "1.0.0", "surface": {"kind": "panel", "instance": "dev-9"}, "accepts": {"reload": {}, "draft": {}}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	hub.broadcast("navigate", struct{}{}) // suppressed: not in accepts

	req := httptest.NewRequest(http.MethodGet, "/api/chat/subscribers", nil)
	rec := httptest.NewRecorder()
	handleSubscribers(st, hub)(rec, req)

	var got subscribersResponse
	decodeBody(t, rec, &got)
	if got.CapabilitySuppressed["navigate"] != 1 {
		t.Errorf("capability_suppressed = %v, want navigate:1", got.CapabilitySuppressed)
	}
	if len(got.CapabilityDeclarations) != 1 {
		t.Fatalf("capability_declarations = %+v, want exactly one entry", got.CapabilityDeclarations)
	}
	d := got.CapabilityDeclarations[0]
	if d.Surface.Kind != "panel" || d.Surface.Instance != "dev-9" || d.Device != "dev-9" || d.ConnectedAt == "" {
		t.Errorf("declaration entry = %+v, want panel/dev-9 with a connectedAt", d)
	}
	if len(d.Accepts) != 2 || d.Accepts[0] != "draft" || d.Accepts[1] != "reload" {
		t.Errorf("accepts = %v, want sorted [draft reload]", d.Accepts)
	}
	if d.Content == nil || d.Interactions == nil {
		t.Errorf("content/interactions = %v/%v, want [] not null (the TS parse defaults both)", d.Content, d.Interactions)
	}
}

func TestHandleSubscribersNilHubCapabilityFieldsPresent(t *testing.T) {
	// The nil-hub path (tests that don't care about SSE) must still produce
	// the fields, empty — absent fields would be a different wire shape.
	req := httptest.NewRequest(http.MethodGet, "/api/chat/subscribers", nil)
	rec := httptest.NewRecorder()
	handleSubscribers(newTestStore(t), nil)(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `"capability_suppressed":{}`) || !strings.Contains(body, `"capability_declarations":[]`) {
		t.Errorf("nil-hub subscribers body missing empty capability fields; got:\n%s", body)
	}
}
