package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestSSEGolden replays the golden scenario against a real Go server (full
// Register mux over live HTTP, so the wire bytes are the real thing) and
// compares the two SSE streams frame-by-frame against
// testdata/sse-golden.json — a normalized capture of the TypeScript server
// running the identical scenario, refreshed by
// parity/refresh-sse-golden.sh (local-only: it boots the TS server, which
// CI's shell job deliberately cannot do; this test itself is hermetic and
// rides the go job).
//
// Every difference between the streams is applied as an explicit transform
// below, each citing its docs/api-contract.md divergence-ledger row or table
// line — an undocumented divergence fails the test, which is the point.

// sseGoldenSteps must match STEPS in parity/capture-to-golden.ts; the test
// cross-checks the golden's recorded list before comparing anything, so a
// scenario edit on one side cannot silently compare mismatched steps.
var sseGoldenSteps = []string{"connect-burst", "register-agent", "poll-park", "send", "reload", "unregister"}

type goldenFrame struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

type goldenCapture struct {
	Steps  []string        `json:"steps"`
	Legacy [][]goldenFrame `json:"legacy"`
	Caps   [][]goldenFrame `json:"caps"`
}

// normalizeValue erases volatile values, rule-for-rule identical to
// normalize() in parity/capture-to-golden.ts: ts/clientId/connectedAt/
// lastSeen values always, id values except the scenario's one stable agent
// id ("golden").
func normalizeValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, inner := range val {
			switch {
			case (k == "ts" || k == "clientId" || k == "connectedAt" || k == "lastSeen") && isString(inner):
				out[k] = "<norm>"
			case k == "id" && inner != "golden":
				out[k] = "<norm>"
			default:
				out[k] = normalizeValue(inner)
			}
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, inner := range val {
			out[i] = normalizeValue(inner)
		}
		return out
	default:
		return v
	}
}

func isString(v any) bool { _, ok := v.(string); return ok }

// transformTSStep maps one golden (TS) step onto what the Go server is
// expected to emit for it. Every rule cites the contract.
func transformTSStep(step []goldenFrame) []goldenFrame {
	out := make([]goldenFrame, 0, len(step))
	for _, f := range step {
		switch f.Event {
		case "draft", "presence":
			// TS emits these around /send (api-contract.md events table:
			// "emitted around send/reply on TS"); the Go server has no
			// producer for either (events.go liveness table).
			continue
		case "presence_map":
			// Defensive: the golden pipeline already excludes presence_map
			// (see parity/capture-to-golden.ts — the TS server rebroadcasts
			// it from a 10s sweep timer, so its arrivals are wall-clock-
			// nondeterministic, and ledger row 3 diverges its vocabulary).
			// The Go burst's presence_map placement is pinned by
			// events_presence_test.go instead.
			continue
		case "connected":
			// Ledger row 1: TS carries clientId, Go has no per-connection
			// public id.
			if m, ok := f.Data.(map[string]any); ok {
				delete(m, "clientId")
			}
		case "message_received":
			// Ledger row 7: TS payload { id, channel? }, Go { id }.
			if m, ok := f.Data.(map[string]any); ok {
				delete(m, "channel")
			}
		case "message":
			// `received` is the TS server's runtime-only queued/delivered
			// flag (api-contract.md:258, "stripped from disk"); the Go
			// ChatMessage does not model it — delivery acks ride
			// message_received alone.
			if m, ok := f.Data.(map[string]any); ok {
				delete(m, "received")
			}
		case "history":
			if msgs, ok := f.Data.([]any); ok {
				for _, msg := range msgs {
					if m, ok := msg.(map[string]any); ok {
						delete(m, "received")
					}
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// transformGoStep drops the frames the golden does not model: the burst
// `commands` snapshot (api-contract.md:754 and table line 780, ledger row
// 29 — Go-only), and presence_map, which the whole golden pipeline excludes
// (see transformTSStep's presence_map case for why).
func transformGoStep(step []goldenFrame) []goldenFrame {
	out := make([]goldenFrame, 0, len(step))
	for _, f := range step {
		if f.Event == "commands" || f.Event == "presence_map" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// sortStep orders frames within a step deterministically. Steps after the
// burst can interleave across goroutines on the Go side (the broker→hub
// bridge vs. the poll handler's own broadcasts), so those steps are compared
// as multisets; the single-writer connect burst keeps its order.
func sortStep(step []goldenFrame) {
	key := func(f goldenFrame) string {
		b, _ := json.Marshal(f.Data)
		return f.Event + "\x00" + string(b)
	}
	sort.Slice(step, func(i, j int) bool { return key(step[i]) < key(step[j]) })
}

// sseTestClient reads one live SSE stream, delivering parsed+normalized
// frames on a channel.
type sseTestClient struct {
	frames chan goldenFrame
	errs   chan error
	cancel context.CancelFunc
}

func openSSEStream(t *testing.T, base, rawQuery string) *sseTestClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/chat/events?"+rawQuery, nil)
	if err != nil {
		cancel()
		t.Fatalf("build events request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("connect events stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("events stream status = %d, want 200", resp.StatusCode)
	}
	c := &sseTestClient{frames: make(chan goldenFrame, 64), errs: make(chan error, 1), cancel: cancel}
	go func() {
		defer resp.Body.Close()
		r := bufio.NewReader(resp.Body)
		var event, data string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				c.errs <- err
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case line == "":
				if event != "" || data != "" {
					var parsed any
					if err := json.Unmarshal([]byte(data), &parsed); err != nil {
						c.errs <- fmt.Errorf("frame %q: bad data %q: %w", event, data, err)
						return
					}
					c.frames <- goldenFrame{Event: event, Data: normalizeValue(parsed)}
					event, data = "", ""
				}
			case strings.HasPrefix(line, ":"): // keepalive comment
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			default:
				c.errs <- fmt.Errorf("unrecognized SSE line %q", line)
				return
			}
		}
	}()
	return c
}

// readStep reads exactly n frames, failing on a stalled stream rather than
// asserting anything about elapsed time.
func readStep(t *testing.T, c *sseTestClient, stream, step string, n int) []goldenFrame {
	t.Helper()
	out := make([]goldenFrame, 0, n)
	for len(out) < n {
		select {
		case f := <-c.frames:
			out = append(out, f)
		case err := <-c.errs:
			t.Fatalf("%s stream failed during %s (have %d/%d frames): %v", stream, step, len(out), n, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("%s stream: timed out during %s with %d/%d frames: %+v", stream, step, len(out), n, out)
		}
	}
	return out
}

// requireQuiet asserts no further frame arrives within the grace window — a
// late leak (e.g. a reload delivered to the caps client after all) would
// otherwise vanish past the last readStep.
func requireQuiet(t *testing.T, c *sseTestClient, stream string) {
	t.Helper()
	select {
	case f := <-c.frames:
		t.Fatalf("%s stream: unexpected trailing frame %q %+v", stream, f.Event, f.Data)
	case <-time.After(300 * time.Millisecond):
	}
}

func postGoldenJSON(t *testing.T, urlStr, body string) {
	t.Helper()
	resp, err := http.Post(urlStr, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", urlStr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status %d", urlStr, resp.StatusCode)
	}
}

func TestSSEGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "sse-golden.json"))
	if err != nil {
		t.Fatalf("read golden (regenerate with parity/refresh-sse-golden.sh): %v", err)
	}
	var golden goldenCapture
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	if !reflect.DeepEqual(golden.Steps, sseGoldenSteps) {
		t.Fatalf("golden steps %v != test scenario %v — regenerate the golden and update both sides together", golden.Steps, sseGoldenSteps)
	}

	st := newTestStore(t)
	mux := http.NewServeMux()
	Register(mux, st)
	srv := httptest.NewServer(mux)
	// LIFO: the open SSE connections must be torn down before Close, which
	// blocks until every connection has drained.
	defer srv.Close()
	defer srv.CloseClientConnections()

	// Same declaration the harness sends: accepts navigate only, so the
	// scenario's reload must be suppressed on this stream.
	capsDecl := url.QueryEscape(`{"schema":"1.0.0","surface":{"kind":"golden_capture"},"accepts":{"navigate":{}}}`)
	legacy := openSSEStream(t, srv.URL, "")
	defer legacy.cancel()
	caps := openSSEStream(t, srv.URL, "caps="+capsDecl)
	defer caps.cancel()

	// Per-step frame counts on the GO wire (before transforms): the burst
	// carries the Go-only commands frame; poll-park has no presence_map
	// re-broadcast; send has no draft/presence.
	legacyCounts := map[string]int{"connect-burst": 6, "register-agent": 1, "poll-park": 1, "send": 3, "reload": 1, "unregister": 1}
	capsCounts := map[string]int{"connect-burst": 6, "register-agent": 1, "poll-park": 1, "send": 3, "reload": 0, "unregister": 1}

	pollDone := make(chan error, 1)
	run := func(step string) {
		switch step {
		case "connect-burst": // the two opens above are the step
		case "register-agent":
			postGoldenJSON(t, srv.URL+"/api/chat/register-agent", `{"id":"golden","name":"Golden","color":"#3FB950"}`)
		case "poll-park":
			go func() {
				resp, err := http.Get(srv.URL + "/api/chat/poll?channel=golden")
				if err != nil {
					pollDone <- err
					return
				}
				defer resp.Body.Close()
				var buf bytes.Buffer
				if _, err := buf.ReadFrom(resp.Body); err != nil {
					pollDone <- err
					return
				}
				if !strings.Contains(buf.String(), "golden message") {
					pollDone <- fmt.Errorf("poll response %q did not carry the sent message", buf.String())
					return
				}
				pollDone <- nil
			}()
		case "send":
			postGoldenJSON(t, srv.URL+"/api/chat/send", `{"text":"golden message","toAgent":"golden"}`)
		case "reload":
			postGoldenJSON(t, srv.URL+"/api/chat/reload", `{}`)
		case "unregister":
			postGoldenJSON(t, srv.URL+"/api/chat/unregister", `{"id":"golden"}`)
		}
	}

	goLegacy := make([][]goldenFrame, 0, len(sseGoldenSteps))
	goCaps := make([][]goldenFrame, 0, len(sseGoldenSteps))
	for _, step := range sseGoldenSteps {
		run(step)
		goLegacy = append(goLegacy, readStep(t, legacy, "legacy", step, legacyCounts[step]))
		goCaps = append(goCaps, readStep(t, caps, "caps", step, capsCounts[step]))
		if step == "send" {
			select {
			case err := <-pollDone:
				if err != nil {
					t.Fatalf("parked poll: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("parked poll did not return after send")
			}
		}
	}
	requireQuiet(t, legacy, "legacy")
	requireQuiet(t, caps, "caps")

	compare := func(stream string, tsSteps, goSteps [][]goldenFrame) {
		t.Helper()
		if len(tsSteps) != len(sseGoldenSteps) {
			t.Fatalf("%s: golden has %d steps, want %d", stream, len(tsSteps), len(sseGoldenSteps))
		}
		for i, name := range sseGoldenSteps {
			want := transformTSStep(tsSteps[i])
			got := transformGoStep(goSteps[i])
			if i > 0 { // burst order is pinned; later steps are multisets
				sortStep(want)
				sortStep(got)
			}
			if !reflect.DeepEqual(want, got) {
				wantJSON, _ := json.MarshalIndent(want, "", "  ")
				gotJSON, _ := json.MarshalIndent(got, "", "  ")
				t.Errorf("%s stream, step %q:\n--- want (TS golden, transformed) ---\n%s\n--- got (Go) ---\n%s", stream, name, wantJSON, gotJSON)
			}
		}
	}
	compare("legacy", golden.Legacy, goLegacy)
	compare("caps", golden.Caps, goCaps)
}
