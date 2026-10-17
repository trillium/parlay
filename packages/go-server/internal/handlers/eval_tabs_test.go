package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// panelEvalBody is the body packages/client/src/{input.ts,commands/dispatcher/up.ts}
// actually POSTs to /api/chat/eval. `nicknames` is a string ARRAY and is ALWAYS
// present — evalTabs() does `nicknames: a.nicknames ?? []` — and the built-in
// `system` agent is always in the list, so no ordinary panel state omits it.
const panelEvalBody = `{
  "streamId": "eval-640a3cf4-3c4e-4fcb-b28a-1f5cf1ce3a6a-9af6654d-9a05-4953-a4d7-ad919de6b4f4",
  "version": 1,
  "text": "clear the box",
  "cursor": {"anchor": 0, "active": 0},
  "reason": "input",
  "voiceEnabled": false,
  "tabs": [{"id": "system", "name": "System", "nicknames": []}],
  "device": "640a3cf4-3c4e-4fcb-b28a-1f5cf1ce3a6a",
  "paVersion": "5.12.0"
}`

// stoppedHub is a hub whose bridge goroutine ends with the test.
func stoppedHub(t *testing.T) *Hub {
	t.Helper()
	h := newHub(newBroker())
	t.Cleanup(h.Stop)
	return h
}

// The relay's request type once declared tabs as []map[string]string, which
// cannot unmarshal `nicknames: []`, so every real panel POST got
// `400 {"error":"invalid JSON body"}` and the phrase engine was unreachable from
// the panel. This pins the panel's actual shape: it must decode AND reach the
// engine with its nicknames intact.
func TestRelayAcceptsRealPanelBodyWithNicknamesArray(t *testing.T) {
	resetStreamTable(t)

	var got map[string]any
	fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("engine received undecodable body %q: %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"v":1,"streamId":"s","seq":1,"baseVersion":1,"actions":[],"engineEvalNs":1}`))
	})

	rec := postEval(t, stoppedHub(t), panelEvalBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("the panel's own body was rejected: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	tabs, ok := got["tabs"].([]any)
	if !ok || len(tabs) != 1 {
		t.Fatalf("tabs did not reach the engine intact: got %#v", got["tabs"])
	}
	tab := tabs[0].(map[string]any)
	if _, ok := tab["nicknames"].([]any); !ok {
		t.Errorf("nicknames reached the engine as %#v, want an array — the engine's Tab.Nicknames is []string", tab["nicknames"])
	}
	if tab["id"] != "system" || tab["name"] != "System" {
		t.Errorf("tab id/name did not survive the relay: %#v", tab)
	}
}

// A panel with real nicknames must round-trip too: the engine resolves agents by
// nickname, so dropping or stringifying them would silently break addressing.
func TestRelayPreservesTabNicknames(t *testing.T) {
	resetStreamTable(t)

	var got string
	fakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"v":1,"streamId":"s","seq":1,"baseVersion":1,"actions":[],"engineEvalNs":1}`))
	})

	rec := postEval(t, stoppedHub(t), `{
      "device":"d1","streamId":"s","version":1,"text":"hey first mate","reason":"input",
      "tabs":[{"id":"demo","name":"Demo","nicknames":["First Mate","Demo One"]}]
    }`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(got, `"nicknames":["First Mate","Demo One"]`) {
		t.Errorf("nicknames were not forwarded verbatim to the engine: %s", got)
	}
}
