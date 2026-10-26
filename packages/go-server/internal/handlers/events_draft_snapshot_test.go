package handlers

import (
	"strings"
	"testing"
	"time"
)

const (
	capsAcceptsDraftSnapshot = `{"schema": "1.0.0", "surface": {"kind": "pwa"}, "accepts": {"draft_snapshot": {}}}`
	capsPanelAccepts         = `{"schema": "1.0.0", "surface": {"kind": "panel"}, "accepts": {"navigate": {}, "reload": {}, "device_cmd": {}, "input_action": {}, "draft": {}}}`
	capsAcceptsNavigate      = `{"schema": "1.0.0", "surface": {"kind": "pwa"}, "accepts": {"navigate": {}}}`
)

func TestConnectSendsStoredDraftSnapshot(t *testing.T) {
	st := newTestStore(t)
	d, err := st.Drafts.Set("hello draft", "writer-1")
	if err != nil {
		t.Fatal(err)
	}
	rec, stop := runEventsTarget(t, st, newHub(newBroker()), capsTarget(capsAcceptsDraftSnapshot))
	time.Sleep(50 * time.Millisecond)
	stop()

	body := rec.Body.String()
	want := `event: draft_snapshot` + "\n" + `data: {"clientId":"writer-1","text":"hello draft","updatedAt":"` + d.UpdatedAt + `"}` + "\n\n"
	if !strings.Contains(body, want) {
		t.Errorf("connect burst missing the draft snapshot frame; want %q in:\n%s", want, body)
	}
	if strings.Contains(body, "event: input_action") {
		t.Errorf("draft snapshot must not ride the input_action envelope path; got:\n%s", body)
	}
	// It rides the connect burst: after commands, before any live traffic.
	if strings.Index(body, "event: commands") > strings.Index(body, "event: draft_snapshot") {
		t.Errorf("draft snapshot must follow the commands frame; got:\n%s", body)
	}
}

func TestConnectSendsEmptyTextWhenNoDraftStored(t *testing.T) {
	rec, stop := runEventsTarget(t, newTestStore(t), newHub(newBroker()), capsTarget(capsAcceptsDraftSnapshot))
	time.Sleep(50 * time.Millisecond)
	stop()

	want := `event: draft_snapshot` + "\n" + `data: {"clientId":"","text":"","updatedAt":""}` + "\n\n"
	if body := rec.Body.String(); !strings.Contains(body, want) {
		t.Errorf("no stored draft must still send an empty-text snapshot (%q); got:\n%s", want, body)
	}
}

func TestDraftSnapshotNotSentWithoutDraftSnapshotAccept(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Drafts.Set("secret", ""); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"legacy (no caps)":                    "/api/chat/events",
		"declared, no draft_snapshot":         capsTarget(capsAcceptsNavigate),
		"panel accepts (input_action, draft)": capsTarget(capsPanelAccepts),
	} {
		rec, stop := runEventsTarget(t, st, newHub(newBroker()), target)
		time.Sleep(50 * time.Millisecond)
		stop()
		if body := rec.Body.String(); strings.Contains(body, "event: draft") || strings.Contains(body, "secret") {
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
	first, cancel, err := hub.subscribeDeclared("", mustDeclaration(t, capsAcceptsDraftSnapshot))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	_, stop := runEventsTarget(t, st, hub, capsTarget(capsAcceptsDraftSnapshot)+"&device=d2")
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
	other, cancel, err := hub.subscribeDeclared("device-a", mustDeclaration(t, capsAcceptsDraftSnapshot))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	rec, stop := runEventsTarget(t, st, hub, capsTarget(capsAcceptsDraftSnapshot)+"&device=device-b")
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

func TestDraftSnapshotEchoedAsRecognizedThenSent(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Drafts.Set("echoed", ""); err != nil {
		t.Fatal(err)
	}
	rec, stop := runEventsTarget(t, st, newHub(newBroker()), capsTarget(capsAcceptsDraftSnapshot))
	time.Sleep(50 * time.Millisecond)
	stop()

	body := rec.Body.String()
	echo := `"capabilities":{"schema":"1.0.0","recognized":["draft_snapshot"],"unknown":[]}`
	echoAt, frameAt := strings.Index(body, echo), strings.Index(body, "event: draft_snapshot\ndata: {\"clientId\":\"\",\"text\":\"echoed\"")
	if echoAt < 0 {
		t.Fatalf("connected echo must list draft_snapshot as recognized (%s); got:\n%s", echo, body)
	}
	if frameAt < echoAt {
		t.Errorf("draft_snapshot frame must follow the recognizing echo; got:\n%s", body)
	}
}
