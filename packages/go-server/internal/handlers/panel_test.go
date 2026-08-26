package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"parlay/go-server/internal/guard"
	"parlay/go-server/internal/store"
)

// newPanelMux wires exactly the panel routes onto a mux backed by a fresh
// temp-dir store, and hands back both so a test can assert on what the
// handler did to persisted state rather than only on the response body.
func newPanelMux(t *testing.T) (*http.ServeMux, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	b := newBroker()
	mux := http.NewServeMux()
	registerPanel(mux, st, b, newHub(b))
	return mux, st
}

func postPanel(t *testing.T, mux *http.ServeMux, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// Declarations are sticky and, critically, the RESPONSE reports the channel
// that is actually in effect rather than echoing the request. A caller that
// re-declares under a different channel has to be able to see that its
// declaration did not take — echoing the request would tell it the opposite.
func TestDeclareChannelIsStickyAndReportsEffectiveChannel(t *testing.T) {
	mux, st := newPanelMux(t)

	rec := postPanel(t, mux, "/api/chat/declare-channel", `{"session_id":"s1","channel":"edgar"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("first declare: status %d, body %s", rec.Code, rec.Body.String())
	}
	var first declareChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first: %v (body %s)", err, rec.Body.String())
	}
	if !first.OK || first.Channel != "edgar" {
		t.Fatalf("first declare = %+v, want ok with channel edgar", first)
	}

	// Same session, different channel: this is the agent WATCHING another
	// channel, not becoming it. The mapping must not move.
	rec = postPanel(t, mux, "/api/chat/declare-channel", `{"session_id":"s1","channel":"firstmate"}`)
	var second declareChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second: %v (body %s)", err, rec.Body.String())
	}
	if second.Channel != "edgar" {
		t.Errorf("re-declare returned channel %q, want the sticky original %q", second.Channel, "edgar")
	}
	if got, _ := st.Channels.ChannelFor("s1"); got != "edgar" {
		t.Errorf("stored channel = %q, want %q — a re-declare overwrote a sticky mapping", got, "edgar")
	}
}

// The declaration has to survive a restart, which is the entire reason it is
// written to disk instead of held in memory: an agent declares once at spawn
// and must not have to re-declare because the server bounced.
func TestDeclareChannelPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()

	st, err := store.Open(store.Config{Dir: dir})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if _, err := st.Channels.Declare("s1", "edgar"); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	st.Messages.Close()

	reopened, err := store.Open(store.Config{Dir: dir})
	if err != nil {
		t.Fatalf("store.Open (reopen): %v", err)
	}
	t.Cleanup(func() { reopened.Messages.Close() })

	got, ok := reopened.Channels.ChannelFor("s1")
	if !ok || got != "edgar" {
		t.Errorf("after reopen ChannelFor(s1) = %q,%v; want edgar,true", got, ok)
	}
}

func TestDeclareChannelRejectsMissingFields(t *testing.T) {
	mux, _ := newPanelMux(t)

	// Whitespace-only counts as missing: a caller sending "  " has supplied no
	// channel, and storing it would create a mapping to a channel no tab has.
	for _, body := range []string{
		`{"session_id":"","channel":"edgar"}`,
		`{"session_id":"s1","channel":""}`,
		`{"session_id":"s1","channel":"   "}`,
	} {
		rec := postPanel(t, mux, "/api/chat/declare-channel", body)
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		if resp["error"] == nil {
			t.Errorf("body %s was accepted; want an error response", body)
		}
	}
}

// /clear with a channel removes only that channel's messages; without one it
// removes everything. The reported counts have to match what actually
// happened, because the caller uses them to decide whether to retry.
func TestClearScopesToChannelAndReportsRealCounts(t *testing.T) {
	mux, st := newPanelMux(t)

	for _, ch := range []string{"a", "a", "b"} {
		if _, err := st.Messages.Append(store.ChatMessage{Role: "user", Text: "x", Channel: ch}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	rec := postPanel(t, mux, "/api/chat/clear", `{"channel":"a"}`)
	var resp clearResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if resp.Removed != 2 || resp.Remaining != 1 {
		t.Errorf("clear channel a = removed %d remaining %d, want 2 and 1", resp.Removed, resp.Remaining)
	}
	if st.Messages.Len() != 1 {
		t.Errorf("store holds %d messages after scoped clear, want 1", st.Messages.Len())
	}

	rec = postPanel(t, mux, "/api/chat/clear", `{}`)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Remaining != 0 || st.Messages.Len() != 0 {
		t.Errorf("unscoped clear left %d (reported %d), want 0", st.Messages.Len(), resp.Remaining)
	}
}

// Message ids are handed to clients as long-poll cursors, so a clear must not
// restart the sequence counter — a reused id makes a held cursor resolve
// against a different message and silently replay the wrong window.
func TestClearDoesNotRecycleMessageIDs(t *testing.T) {
	mux, st := newPanelMux(t)

	// Several messages before the clear, and several after: a counter reset
	// to 1 collides with the SECOND id issued, not the first, so a
	// one-message-either-side version of this test passes even with the
	// counter being reset.
	issued := map[string]bool{}
	for i := 0; i < 3; i++ {
		m, err := st.Messages.Append(store.ChatMessage{Role: "user", Text: "before", Channel: "a"})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		issued[m.ID] = true
	}

	if rec := postPanel(t, mux, "/api/chat/clear", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("clear: status %d", rec.Code)
	}

	for i := 0; i < 3; i++ {
		m, err := st.Messages.Append(store.ChatMessage{Role: "user", Text: "after", Channel: "a"})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if issued[m.ID] {
			t.Fatalf("message id %q was reissued after a clear; ids back a long-poll cursor and must never repeat", m.ID)
		}
		issued[m.ID] = true
	}
}

// The 500-character cap counts characters, not bytes. Cutting the byte slice
// would both split a multi-byte rune and truncate a non-ASCII message far
// short of its real length.
func TestSystemTruncatesOnRuneBoundary(t *testing.T) {
	mux, st := newPanelMux(t)

	long := strings.Repeat("é", 600) // 600 runes, 1200 bytes
	body, err := json.Marshal(systemRequest{Text: long})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if rec := postPanel(t, mux, "/api/chat/system", string(body)); rec.Code != http.StatusOK {
		t.Fatalf("system: status %d, body %s", rec.Code, rec.Body.String())
	}

	msgs := st.Messages.History(0)
	if len(msgs) != 1 {
		t.Fatalf("stored %d messages, want 1", len(msgs))
	}
	got := msgs[0].Text
	if !utf8.ValidString(got) {
		t.Errorf("stored text is not valid UTF-8 — the cut split a rune")
	}
	if n := utf8.RuneCountInString(got); n != 500 {
		t.Errorf("stored %d runes, want 500 (a byte-wise cut would give 250)", n)
	}
}

func TestSystemRequiresText(t *testing.T) {
	mux, st := newPanelMux(t)

	rec := postPanel(t, mux, "/api/chat/system", `{"text":""}`)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] == nil {
		t.Errorf("empty text was accepted; want an error")
	}
	if st.Messages.Len() != 0 {
		t.Errorf("an empty system message was stored anyway")
	}
}

func TestNavigateRequiresURL(t *testing.T) {
	mux, _ := newPanelMux(t)

	rec := postPanel(t, mux, "/api/chat/navigate", `{"url":""}`)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] == nil {
		t.Errorf("empty url was accepted; want an error")
	}
}

func TestDeviceCmdRequiresCmd(t *testing.T) {
	mux, _ := newPanelMux(t)

	rec := postPanel(t, mux, "/api/chat/device-cmd", `{"cmd":""}`)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] == nil {
		t.Errorf("empty cmd was accepted; want an error")
	}
}

// Every panel route that mutates something rejects a non-POST. /version is
// the one read-only route and rejects everything but GET.
func TestPanelRouteMethods(t *testing.T) {
	mux, _ := newPanelMux(t)

	for _, path := range []string{
		"/api/chat/clear", "/api/chat/navigate", "/api/chat/reload",
		"/api/chat/device-cmd", "/api/chat/system", "/api/chat/declare-channel",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, want 405", path, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/chat/version", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/chat/version = %d, want 405", rec.Code)
	}
}

// Every panel route that mutates state or drives the captain's device has to
// be in the guard list. This is the check that catches a route added later
// and left unguarded — the chat API has no authentication, so the guard list
// IS the boundary.
func TestPanelRoutesAreGuarded(t *testing.T) {
	for _, path := range []string{
		"/api/chat/clear", "/api/chat/navigate", "/api/chat/reload",
		"/api/chat/device-cmd", "/api/chat/system", "/api/chat/declare-channel",
	} {
		if !guard.IsGuarded(path) {
			t.Errorf("%s mutates or drives the device but is not guarded", path)
		}
	}
}
