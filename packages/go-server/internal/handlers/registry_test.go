package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"parlay/go-server/internal/store"
)

func TestHandleRegisterAgentRequiresID(t *testing.T) {
	rec := postJSON(t, handleRegisterAgent(newTestStore(t), nil), map[string]any{"name": "no id"})
	var got map[string]string
	decodeBody(t, rec, &got)
	if rec.Code != http.StatusOK || got["error"] == "" {
		t.Errorf("status=%d body=%v, want 200 with an error field", rec.Code, got)
	}
}

func TestHandleRegisterAgentUpsertsAndReturnsNicknames(t *testing.T) {
	st := newTestStore(t)
	rec := postJSON(t, handleRegisterAgent(st, nil), map[string]any{
		"id": "c1", "name": "Firstmate", "color": "#abcdef", "nicknames": []string{"fm"},
	})
	var got registerAgentResponse
	decodeBody(t, rec, &got)
	if !got.OK || len(got.Nicknames) != 1 || got.Nicknames[0] != "fm" {
		t.Errorf("response = %+v, want ok=true nicknames=[fm]", got)
	}
	agent, ok := st.Registry.Get("c1")
	if !ok || agent.Name != "Firstmate" || agent.Color != "#abcdef" {
		t.Errorf("Registry.Get(c1) = %+v, ok=%v, want Name=Firstmate Color=#abcdef", agent, ok)
	}
}

func TestHandleUnregisterUnknownIDReturns404(t *testing.T) {
	rec := postJSON(t, handleUnregister(newTestStore(t), nil), map[string]any{"id": "ghost"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleUnregisterMissingIDReturns400(t *testing.T) {
	rec := postJSON(t, handleUnregister(newTestStore(t), nil), map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleUnregisterRemovesRegisteredAgent(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Registry.Upsert(store.AgentInfo{ID: "c1"}); err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, handleUnregister(st, nil), map[string]any{"id": "c1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got unregisterResponse
	decodeBody(t, rec, &got)
	if !got.OK || got.ID != "c1" {
		t.Errorf("response = %+v, want {ok:true id:c1}", got)
	}
	if _, ok := st.Registry.Get("c1"); ok {
		t.Error("Registry.Get(c1) still found after unregister")
	}
}

func TestHandleUnregisterBroadcastsAgentUnregister(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Registry.Upsert(store.AgentInfo{ID: "c1"}); err != nil {
		t.Fatal(err)
	}
	hub := newHub(newBroker())
	sub, cancel := hub.subscribe("")
	defer cancel()

	rec := postJSON(t, handleUnregister(st, hub), map[string]any{"id": "c1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	select {
	case ev := <-sub:
		if ev.name != eventAgentUnregister {
			t.Fatalf("broadcast event = %q, want %q", ev.name, eventAgentUnregister)
		}
		payload, ok := ev.data.(map[string]string)
		if !ok || payload["id"] != "c1" {
			t.Errorf("broadcast payload = %#v, want map with id=c1", ev.data)
		}
	case <-time.After(time.Second):
		t.Fatal("hub did not receive agent_unregister event")
	}
}

func deleteAgentRequest(t *testing.T, st *store.Store, hub *Hub, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, target, nil)
	rec := httptest.NewRecorder()
	handleDeleteAgent(st, hub)(rec, req)
	return rec
}

func TestHandleDeleteAgentRemovesAndEchoesID(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Registry.Upsert(store.AgentInfo{ID: "c1"}); err != nil {
		t.Fatal(err)
	}
	hub := newHub(newBroker())
	sub, cancel := hub.subscribe("")
	defer cancel()

	rec := deleteAgentRequest(t, st, hub, "/api/chat/agents/c1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got unregisterResponse
	decodeBody(t, rec, &got)
	if !got.OK || got.ID != "c1" {
		t.Errorf("response = %+v, want {ok:true id:c1}", got)
	}
	if _, ok := st.Registry.Get("c1"); ok {
		t.Error("Registry.Get(c1) still found after DELETE")
	}
	select {
	case ev := <-sub:
		if ev.name != eventAgentUnregister {
			t.Errorf("broadcast event = %q, want %q", ev.name, eventAgentUnregister)
		}
	case <-time.After(time.Second):
		t.Fatal("hub did not receive agent_unregister event")
	}
}

func TestHandleDeleteAgentUnknownIDReturns404(t *testing.T) {
	rec := deleteAgentRequest(t, newTestStore(t), nil, "/api/chat/agents/ghost")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleDeleteAgentEmptyIDReturns400(t *testing.T) {
	// Both the bare subtree root and a whitespace-only segment (the TS side
	// trims after URL-decoding) are "id required", not a lookup.
	for _, target := range []string{"/api/chat/agents/", "/api/chat/agents/%20%20"} {
		rec := deleteAgentRequest(t, newTestStore(t), nil, target)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("DELETE %s status = %d, want 400", target, rec.Code)
		}
	}
}

func TestHandleDeleteAgentWrongMethodReturns405(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/chat/agents/c1", nil)
	rec := httptest.NewRecorder()
	handleDeleteAgent(newTestStore(t), nil)(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodDelete {
		t.Errorf("status = %d Allow = %q, want 405 with Allow: DELETE", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestHandleAgentsListsRegistered(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Registry.Upsert(store.AgentInfo{ID: "c1", Name: "Firstmate"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/chat/agents", nil)
	rec := httptest.NewRecorder()
	handleAgents(st)(rec, req)

	var got []store.AgentInfo
	decodeBody(t, rec, &got)
	if len(got) != 1 || got[0].ID != "c1" || got[0].Name != "Firstmate" {
		t.Errorf("agents = %+v, want [{ID:c1 Name:Firstmate}]", got)
	}
}

func TestHandleSubscribersReportsPollAndRegisteredAndPresence(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Registry.Upsert(store.AgentInfo{ID: "c1", Name: "Firstmate"}); err != nil {
		t.Fatal(err)
	}
	st.Presence.AddPoller("c1")
	st.Presence.Touch("c1", "2026-08-03T00:00:00Z")

	req := httptest.NewRequest(http.MethodGet, "/api/chat/subscribers", nil)
	rec := httptest.NewRecorder()
	handleSubscribers(st)(rec, req)

	var got subscribersResponse
	decodeBody(t, rec, &got)

	if got.Registered.Count != 1 || len(got.Registered.Agents) != 1 || got.Registered.Agents[0].ID != "c1" {
		t.Errorf("Registered = %+v, want count=1 agents=[c1]", got.Registered)
	}
	if got.Poll.Count != 1 || len(got.Poll.Channels) != 1 {
		t.Fatalf("Poll = %+v, want count=1 with one channel entry", got.Poll)
	}
	pc := got.Poll.Channels[0]
	if pc.Channel == nil || *pc.Channel != "c1" || pc.ID != "c1" || pc.Name != "Firstmate" {
		t.Errorf("Poll.Channels[0] = %+v, want channel=c1 id=c1 name=Firstmate", pc)
	}
	if len(got.Presence) != 1 || got.Presence[0].Channel != "c1" || got.Presence[0].LastSeen == nil || *got.Presence[0].LastSeen != "2026-08-03T00:00:00Z" {
		t.Errorf("Presence = %+v, want one entry for c1 with the touched timestamp", got.Presence)
	}
}

func TestHandleSubscribersDefaultChannelReportsNullChannel(t *testing.T) {
	st := newTestStore(t)
	st.Presence.AddPoller("")

	req := httptest.NewRequest(http.MethodGet, "/api/chat/subscribers", nil)
	rec := httptest.NewRecorder()
	handleSubscribers(st)(rec, req)

	var got subscribersResponse
	decodeBody(t, rec, &got)
	if len(got.Poll.Channels) != 1 || got.Poll.Channels[0].Channel != nil {
		t.Errorf("Poll.Channels = %+v, want one entry with channel=null for the default channel", got.Poll.Channels)
	}
}
