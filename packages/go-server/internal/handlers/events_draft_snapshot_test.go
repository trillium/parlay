package handlers

import (
	"strings"
	"testing"
	"time"
)

const (
	capsAcceptsInputAction = `{"schema": "1.0.0", "surface": {"kind": "pwa"}, "accepts": {"input_action": {}}}`
	capsAcceptsNavigate    = `{"schema": "1.0.0", "surface": {"kind": "pwa"}, "accepts": {"navigate": {}}}`
)

func TestConnectSendsStoredDraftAsSetText(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Drafts.Set("hello draft", "writer-1"); err != nil {
		t.Fatal(err)
	}
	rec, stop := runEventsTarget(t, st, newHub(newBroker()), capsTarget(capsAcceptsInputAction))
	time.Sleep(50 * time.Millisecond)
	stop()

	body := rec.Body.String()
	want := `event: input_action` + "\n" + `data: {"actions":[{"args":{"text":"hello draft"},"verb":"setText"}],"baseVersion":1000000000,"seq":0,"streamId":"draft","timing":{"connectSnapshot":true},"v":1}` + "\n\n"
	if !strings.Contains(body, want) {
		t.Errorf("connect burst missing the draft setText frame; want %q in:\n%s", want, body)
	}
	// It rides the connect burst: after commands, before any live traffic.
	if strings.Index(body, "event: commands") > strings.Index(body, "event: input_action") {
		t.Errorf("draft snapshot must follow the commands frame; got:\n%s", body)
	}
}

func TestConnectSendsExplicitClearWhenDraftEmpty(t *testing.T) {
	rec, stop := runEventsTarget(t, newTestStore(t), newHub(newBroker()), capsTarget(capsAcceptsInputAction))
	time.Sleep(50 * time.Millisecond)
	stop()

	want := `"actions":[{"verb":"clear"}]`
	if body := rec.Body.String(); !strings.Contains(body, "event: input_action") || !strings.Contains(body, want) {
		t.Errorf("empty draft must be an explicit clear (%s); got:\n%s", want, body)
	}
}

func TestDraftSnapshotNotSentWithoutInputActionCapability(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Drafts.Set("secret", ""); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"legacy (no caps)":          "/api/chat/events",
		"declared, no input_action": capsTarget(capsAcceptsNavigate),
	} {
		rec, stop := runEventsTarget(t, st, newHub(newBroker()), target)
		time.Sleep(50 * time.Millisecond)
		stop()
		if body := rec.Body.String(); strings.Contains(body, "event: input_action") || strings.Contains(body, "secret") {
			t.Errorf("%s: draft snapshot leaked; got:\n%s", name, body)
		}
	}
}

func TestDraftSnapshotIsPerConnectionNotBroadcast(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Drafts.Set("only on connect", ""); err != nil {
		t.Fatal(err)
	}
	hub := newHub(newBroker())
	t.Cleanup(hub.Stop)

	// An already-connected declared client sees nothing when a second client
	// connects: the snapshot is written to the new connection, not fanned out.
	first, cancel, err := hub.subscribeDeclared("", mustDeclaration(t, capsAcceptsInputAction))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	_, stop := runEventsTarget(t, st, hub, capsTarget(capsAcceptsInputAction)+"&device=d2")
	time.Sleep(50 * time.Millisecond)
	stop()

	select {
	case ev := <-first:
		t.Errorf("existing subscriber received %q; the snapshot must not go through broadcast", ev.name)
	default:
	}

	// And a PUT-style draft change after connect pushes nothing either.
	if _, err := st.Drafts.Set("later", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-first:
		t.Errorf("draft write produced SSE event %q; none is expected", ev.name)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestDraftSnapshotScopedToConnectingDevice(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Drafts.Set("for the new tab", ""); err != nil {
		t.Fatal(err)
	}
	hub := newHub(newBroker())
	t.Cleanup(hub.Stop)
	other, cancel, err := hub.subscribeDeclared("device-a", mustDeclaration(t, capsAcceptsInputAction))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	rec, stop := runEventsTarget(t, st, hub, capsTarget(capsAcceptsInputAction)+"&device=device-b")
	time.Sleep(50 * time.Millisecond)
	stop()

	if !strings.Contains(rec.Body.String(), "for the new tab") {
		t.Errorf("connecting device-b did not get its snapshot; got:\n%s", rec.Body.String())
	}
	select {
	case ev := <-other:
		t.Errorf("device-a received %q from device-b's connect", ev.name)
	default:
	}
}
