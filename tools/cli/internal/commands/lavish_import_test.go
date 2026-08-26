package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// sseFrame renders a chat array as the single `data:` frame Lavish sends.
func sseFrame(t *testing.T, msgs []lavishMsg) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"chat": msgs})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return "event: state\ndata: " + string(payload) + "\n\n"
}

func TestFetchLavishChatHistoryReadsFirstFrame(t *testing.T) {
	want := []lavishMsg{
		{Role: "user", Text: "hello", At: "2026-08-26T12:00:00Z"},
		{Role: "agent", Text: "hi back", At: "2026-08-26T12:00:01Z"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events/abc123" {
			t.Errorf("Lavish got path %q, want /events/abc123", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": comment line the reader must skip\n\n")
		fmt.Fprint(w, sseFrame(t, want))
		w.(http.Flusher).Flush()
	}))
	defer srv.Close()

	got, err := fetchLavishChatHistory(srv.URL, "abc123")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) != 2 || got[0].Text != "hello" || got[1].Role != "agent" {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// The whole transcript arrives as ONE data frame. bufio.Scanner's 64 KiB
// default made any session past a few dozen messages abort with "token too
// long" — and the caller reports that as "no messages", so a long session
// imported silently as nothing.
func TestFetchLavishChatHistoryHandlesTranscriptOver64KiB(t *testing.T) {
	var big []lavishMsg
	for i := 0; i < 400; i++ {
		big = append(big, lavishMsg{
			Role: "user",
			Text: strings.Repeat("x", 300), // ~120 KB of transcript in one frame
			At:   "2026-08-26T12:00:00Z",
		})
	}
	frame := sseFrame(t, big)
	if len(frame) < 64*1024 {
		t.Fatalf("test fixture is only %d bytes — it does not cross the 64 KiB line the bug lives at", len(frame))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, frame)
		w.(http.Flusher).Flush()
	}))
	defer srv.Close()

	got, err := fetchLavishChatHistory(srv.URL, "big")
	if err != nil {
		t.Fatalf("a %d byte transcript failed to parse: %v", len(frame), err)
	}
	if len(got) != len(big) {
		t.Fatalf("imported %d of %d messages — the long tail was dropped silently", len(got), len(big))
	}
}

// A stream that accepts the connection and then goes quiet must not hang. The
// original guarded this with a select/default inside the read loop, which a
// blocked Scan() never comes back round to check.
func TestFetchLavishChatHistoryTimesOutOnASilentStream(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer func() { close(release); srv.Close() }()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := fetchLavishChatHistory(srv.URL, "quiet")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a silent stream returned success; the deadline did not fire")
		}
		if elapsed := time.Since(start); elapsed > firstFrameTimeout+2*time.Second {
			t.Errorf("took %s to give up, want ~%s", elapsed, firstFrameTimeout)
		}
	case <-time.After(firstFrameTimeout + 5*time.Second):
		t.Fatal("fetchLavishChatHistory hung on a silent stream")
	}
}

// A Lavish that answers but answers badly is the dangerous case: its error page
// carries no "data: " frame, so a reader that only looks for frames falls off
// the end of the stream and reports "no messages" — a broken server and an
// empty session are indistinguishable, and the import silently does nothing.
func TestFetchLavishChatHistoryReportsAnHTTPErrorRatherThanNoMessages(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusBadGateway} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "upstream is unhappy", code)
		}))

		msgs, err := fetchLavishChatHistory(srv.URL, "sess1234")
		srv.Close()

		if err == nil {
			t.Errorf("HTTP %d returned (%d msgs, nil error), want an error", code, len(msgs))
			continue
		}
		if !strings.Contains(err.Error(), fmt.Sprint(code)) {
			t.Errorf("HTTP %d produced %q, which does not name the status", code, err)
		}
	}
}

func TestFetchLavishChatHistoryReportsAnUnreachableLavish(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	if _, err := fetchLavishChatHistory(url, "gone"); err == nil {
		t.Fatal("an unreachable Lavish returned no error")
	}
}

// replayToParlay used to read r.StatusCode before ruling out a transport
// error, so it dereferenced a nil response and PANICKED on exactly the case it
// exists to report: Parlay not answering. A panic here also aborts the import
// mid-session, so messages already sent are not reported.
func TestReplayToParlaySurvivesAnUnreachableParlay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("replayToParlay panicked when Parlay was unreachable: %v", p)
		}
	}()
	replayToParlay(url, "sess1234", "session.md", []lavishMsg{
		{Role: "user", Text: "one", At: "2026-08-26T12:00:00Z"},
		{Role: "agent", Text: "two", At: "2026-08-26T12:00:01Z"},
	})
}

// Agent turns go to /api/chat/reply carrying the lavish identity; user turns go
// to /api/chat/send carrying only text. Getting this backwards would attribute
// the captain's own messages to the agent.
func TestReplayToParlayRoutesRolesToTheRightEndpoint(t *testing.T) {
	type hit struct {
		path string
		body map[string]any
	}
	// httptest runs each handler call on its own goroutine, so every touch of
	// `hits` — including the assertions below — needs the lock.
	var mu sync.Mutex
	var hits []hit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		hits = append(hits, hit{r.URL.Path, body})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	replayToParlay(srv.URL, "sess1234", "session.md", []lavishMsg{
		{Role: "agent", Text: "from the agent", At: "2026-08-26T12:00:00Z"},
		{Role: "user", Text: "from the captain", At: "2026-08-26T12:00:01Z"},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 2 {
		t.Fatalf("got %d posts, want 2", len(hits))
	}
	if hits[0].path != "/api/chat/reply" {
		t.Errorf("agent turn went to %s, want /api/chat/reply", hits[0].path)
	}
	if hits[0].body["agent"] != "lavish" || hits[0].body["name"] != "Lavish" {
		t.Errorf("agent turn lost its identity: %+v", hits[0].body)
	}
	if hits[1].path != "/api/chat/send" {
		t.Errorf("user turn went to %s, want /api/chat/send", hits[1].path)
	}
	if _, hasAgent := hits[1].body["agent"]; hasAgent {
		t.Errorf("user turn was attributed to an agent: %+v", hits[1].body)
	}
}

// A non-2xx must be reported per-message rather than aborting the import, and a
// 1xx must not be mistaken for success.
func TestReplayToParlayContinuesPastAFailedMessage(t *testing.T) {
	var mu sync.Mutex
	var seen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen++
		first := seen == 1
		mu.Unlock()
		if first {
			http.Error(w, "nope", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	replayToParlay(srv.URL, "sess1234", "session.md", []lavishMsg{
		{Role: "user", Text: "first", At: "2026-08-26T12:00:00Z"},
		{Role: "user", Text: "second", At: "2026-08-26T12:00:01Z"},
		{Role: "user", Text: "third", At: "2026-08-26T12:00:02Z"},
	})

	if seen != 3 {
		t.Errorf("stopped after %d messages; a failed send must not abandon the rest", seen)
	}
}
