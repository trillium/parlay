package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"parlay/go-server/internal/linkrewrite"
	"parlay/go-server/internal/store"
)

// withPublicHost configures linkrewrite for the duration of the test and
// restores the prior (unconfigured, "no rewrite") state on cleanup.
func withPublicHost(t *testing.T, host string) {
	t.Helper()
	restore := linkrewrite.SetGetenvForTest(func(key string) string {
		if key == "PARLAY_PUBLIC_HOST" {
			return host
		}
		return ""
	})
	linkrewrite.ResetCacheForTest()
	t.Cleanup(func() {
		restore()
		linkrewrite.ResetCacheForTest()
	})
}

func TestHandleHistoryRewritesLocalhostLinksWhenConfigured(t *testing.T) {
	withPublicHost(t, "macbook")
	st := newTestStore(t)
	if _, err := st.Messages.Append(store.ChatMessage{Role: "agent", Text: "see http://localhost:4242/panel"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/chat/history", nil)
	rec := httptest.NewRecorder()
	handleHistory(st)(rec, req)

	var got []store.ChatMessage
	decodeBody(t, rec, &got)
	if len(got) != 1 || got[0].Text != "see http://macbook:4242/panel" {
		t.Fatalf("history = %+v, want rewritten link", got)
	}

	// The durable log itself must never be mutated by serving a rewritten view.
	if stored := st.Messages.History(0); stored[0].Text != "see http://localhost:4242/panel" {
		t.Fatalf("stored text = %q, want untouched original", stored[0].Text)
	}
}

func TestHandleHistoryUnconfiguredLeavesLinksUntouched(t *testing.T) {
	withPublicHost(t, "")
	st := newTestStore(t)
	if _, err := st.Messages.Append(store.ChatMessage{Role: "agent", Text: "see http://localhost:4242/panel"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/chat/history", nil)
	rec := httptest.NewRecorder()
	handleHistory(st)(rec, req)

	var got []store.ChatMessage
	decodeBody(t, rec, &got)
	if len(got) != 1 || got[0].Text != "see http://localhost:4242/panel" {
		t.Fatalf("history = %+v, want byte-identical legacy behavior", got)
	}
}

func TestToPollMessageRewritesTextWhenConfigured(t *testing.T) {
	withPublicHost(t, "macbook")
	got := toPollMessage(store.ChatMessage{ID: "m1", Role: "user", Text: "http://127.0.0.1:9999/x"})
	if got.Text != "http://macbook:9999/x" {
		t.Fatalf("poll text = %q, want rewritten", got.Text)
	}
}

func TestToPollMessageUnconfiguredLeavesTextUntouched(t *testing.T) {
	withPublicHost(t, "")
	got := toPollMessage(store.ChatMessage{ID: "m1", Role: "user", Text: "http://127.0.0.1:9999/x"})
	if got.Text != "http://127.0.0.1:9999/x" {
		t.Fatalf("poll text = %q, want byte-identical legacy behavior", got.Text)
	}
}
