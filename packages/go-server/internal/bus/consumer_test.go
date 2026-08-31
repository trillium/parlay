package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGCEvents writes a shell script standing in for `gc events --follow`.
// Each invocation appends its argv to capturePath (same record format as
// emitter_test's fakeGC), then cats fixture.<n> (1-based per invocation) to
// stdout if it exists. tail decides what happens after the fixture: "hang"
// sleeps until killed (a healthy follow stream that has gone quiet), "exit"
// exits immediately (a died supervisor, driving the respawn path).
func fakeGCEvents(t *testing.T, dir, capturePath, tail string) string {
	t.Helper()
	countPath := filepath.Join(dir, "count")
	script := "#!/bin/sh\n" +
		"n=$(cat \"" + countPath + "\" 2>/dev/null || echo 0)\n" +
		"n=$((n+1))\n" +
		"echo \"$n\" > \"" + countPath + "\"\n" +
		"{\n" +
		"  for a in \"$@\"; do printf 'arg:%s\\n' \"$a\"; done\n" +
		"  printf 'END\\n'\n" +
		"} >> \"" + capturePath + "\"\n" +
		"[ -f \"" + dir + "/fixture.$n\" ] && cat \"" + dir + "/fixture.$n\"\n"
	switch tail {
	case "hang":
		script += "sleep 600\n"
	case "exit":
		script += "exit 0\n"
	default:
		t.Fatalf("unknown tail %q", tail)
	}
	bin := filepath.Join(dir, "gc")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gc: %v", err)
	}
	return bin
}

func writeFixture(t *testing.T, dir string, n int, lines ...string) {
	t.Helper()
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("fixture.%d", n)), []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// busLine renders one gc JSONL event line.
func busLine(seq uint64, typ, actor, payload string) string {
	return fmt.Sprintf(`{"actor":%q,"seq":%d,"ts":"2026-08-30T12:00:00Z","type":%q,"ok":true,"payload":%s}`, actor, seq, typ, payload)
}

// delivery is one Broadcast call the consumer made.
type delivery struct {
	name    string
	payload string
}

// recordingBroadcast mimics Hub.BroadcastFromBus's allowlist contract:
// names in allow are accepted and recorded, everything else is refused.
// Deliveries land on ch so tests can wait for them without polling.
func recordingBroadcast(allow map[string]bool, ch chan delivery) func(string, json.RawMessage) bool {
	return func(name string, data json.RawMessage) bool {
		if !allow[name] {
			return false
		}
		ch <- delivery{name: name, payload: string(data)}
		return true
	}
}

// collect reads n deliveries or fails the test after a liveness timeout
// (a wait bound, not an elapsed-time assertion).
func collect(t *testing.T, ch chan delivery, n int) []delivery {
	t.Helper()
	var out []delivery
	for len(out) < n {
		select {
		case d := <-ch:
			out = append(out, d)
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for delivery %d/%d (got %v)", len(out)+1, n, out)
		}
	}
	return out
}

var testAllow = map[string]bool{"message": true, "tool_event": true}

func startTestConsumer(t *testing.T, gcBin, city, cursorPath string, ch chan delivery) *Consumer {
	t.Helper()
	c, err := StartConsumer(ConsumerConfig{
		GCBin:      gcBin,
		CityPath:   city,
		CursorPath: cursorPath,
		Broadcast:  recordingBroadcast(testAllow, ch),
		Backoff:    10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("StartConsumer: %v", err)
	}
	return c
}

func TestConsumerDeliversParlayEventsStrippedAndVerbatim(t *testing.T) {
	city := newTestCity(t)
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.txt")
	writeFixture(t, dir, 1,
		busLine(1, "parlay.message", "other-producer", `{"id":"m1","text":"hi"}`),
		busLine(2, "task.created", "gc-native", `{"x":1}`), // no parlay. prefix: ignored
		busLine(3, "parlay.tool_event", "tailer", `{"tool":"Bash"}`),
	)
	ch := make(chan delivery, 16)
	c := startTestConsumer(t, fakeGCEvents(t, dir, capture, "hang"), city, filepath.Join(dir, "cursor.json"), ch)
	defer c.Close()

	got := collect(t, ch, 2)
	want := []delivery{
		{name: "message", payload: `{"id":"m1","text":"hi"}`},
		{name: "tool_event", payload: `{"tool":"Bash"}`},
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("delivery %d: want %+v, got %+v", i, w, got[i])
		}
	}

	// First run with no cursor: no --after — gc itself resolves the head
	// and tails, so retained history is never replayed at SSE clients.
	recs := readCapture(t, capture)
	if len(recs) == 0 {
		t.Fatal("fake gc never invoked")
	}
	wantArgs := []string{"events", "--follow"}
	if len(recs[0].args) != len(wantArgs) {
		t.Fatalf("argv: want %v, got %v", wantArgs, recs[0].args)
	}
	for i, w := range wantArgs {
		if recs[0].args[i] != w {
			t.Fatalf("argv[%d]: want %q, got %q", i, w, recs[0].args[i])
		}
	}
}

func TestConsumerSkipsSelfOriginButStillAdvancesCursor(t *testing.T) {
	city := newTestCity(t)
	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "cursor.json")
	writeFixture(t, dir, 1,
		busLine(7, "parlay.message", emitActor, `{"id":"self"}`), // this server's own dual-write echo
		busLine(8, "parlay.message", "other", `{"id":"other"}`),
	)
	ch := make(chan delivery, 16)
	c := startTestConsumer(t, fakeGCEvents(t, dir, filepath.Join(dir, "capture.txt"), "hang"), city, cursorPath, ch)
	defer c.Close()

	got := collect(t, ch, 1)
	if got[0].payload != `{"id":"other"}` {
		t.Fatalf("self-origin event was delivered: %+v", got)
	}
	// The skipped self event still advanced and persisted the cursor.
	waitForCursor(t, cursorPath, 8)
}

func TestConsumerDedupesBySeqAndPersistsCursor(t *testing.T) {
	city := newTestCity(t)
	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "cursor.json")
	writeFixture(t, dir, 1,
		busLine(1, "parlay.message", "p", `{"n":1}`),
		busLine(2, "parlay.message", "p", `{"n":2}`),
		busLine(2, "parlay.message", "p", `{"n":2}`), // at-least-once replay
		busLine(1, "parlay.message", "p", `{"n":1}`), // regression
		"not json at all",                            // malformed: skipped, stream continues
		busLine(3, "parlay.message", "p", `{"n":3}`),
	)
	ch := make(chan delivery, 16)
	c := startTestConsumer(t, fakeGCEvents(t, dir, filepath.Join(dir, "capture.txt"), "hang"), city, cursorPath, ch)
	defer c.Close()

	got := collect(t, ch, 3)
	for i, want := range []string{`{"n":1}`, `{"n":2}`, `{"n":3}`} {
		if got[i].payload != want {
			t.Errorf("delivery %d: want %s, got %s", i, want, got[i].payload)
		}
	}
	select {
	case d := <-ch:
		t.Fatalf("duplicate delivery: %+v", d)
	default:
	}
	waitForCursor(t, cursorPath, 3)
}

func TestConsumerResumesFromPersistedCursor(t *testing.T) {
	city := newTestCity(t)
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.txt")
	cursorPath := filepath.Join(dir, "cursor.json")
	if err := os.WriteFile(cursorPath, []byte(`{"after_seq":42}`), 0o644); err != nil {
		t.Fatalf("seed cursor: %v", err)
	}
	writeFixture(t, dir, 1,
		busLine(41, "parlay.message", "p", `{"n":41}`), // below cursor: replay, deduped
		busLine(42, "parlay.message", "p", `{"n":42}`),
		busLine(43, "parlay.message", "p", `{"n":43}`),
	)
	ch := make(chan delivery, 16)
	c := startTestConsumer(t, fakeGCEvents(t, dir, capture, "hang"), city, cursorPath, ch)
	defer c.Close()

	got := collect(t, ch, 1)
	if got[0].payload != `{"n":43}` {
		t.Fatalf("want only seq 43 delivered, got %+v", got)
	}
	recs := readCapture(t, capture)
	wantArgs := []string{"events", "--follow", "--after", "42"}
	if len(recs) == 0 || strings.Join(recs[0].args, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("argv: want %v, got %v", wantArgs, recs)
	}
}

func TestConsumerRespawnsAfterExitAndResumes(t *testing.T) {
	city := newTestCity(t)
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.txt")
	// Invocation 1 emits seq 1-2 then exits (dead supervisor); invocation 2
	// replays seq 2 (at-least-once) then continues.
	writeFixture(t, dir, 1,
		busLine(1, "parlay.message", "p", `{"n":1}`),
		busLine(2, "parlay.message", "p", `{"n":2}`),
	)
	writeFixture(t, dir, 2,
		busLine(2, "parlay.message", "p", `{"n":2}`),
		busLine(3, "parlay.message", "p", `{"n":3}`),
	)
	ch := make(chan delivery, 16)
	c := startTestConsumer(t, fakeGCEvents(t, dir, capture, "exit"), city, filepath.Join(dir, "cursor.json"), ch)
	defer c.Close()

	got := collect(t, ch, 3)
	for i, want := range []string{`{"n":1}`, `{"n":2}`, `{"n":3}`} {
		if got[i].payload != want {
			t.Errorf("delivery %d: want %s, got %s", i, want, got[i].payload)
		}
	}
	// The respawned invocation resumed from the persisted high-water mark.
	recs := readCapture(t, capture)
	if len(recs) < 2 {
		t.Fatalf("want >=2 invocations, got %d", len(recs))
	}
	second := strings.Join(recs[1].args, " ")
	if second != "events --follow --after 2" {
		t.Fatalf("respawn argv: want resume from 2, got %q", second)
	}
}

func TestConsumerCloseKillsSubprocessAndReturns(t *testing.T) {
	city := newTestCity(t)
	dir := t.TempDir()
	writeFixture(t, dir, 1, busLine(1, "parlay.message", "p", `{"n":1}`))
	ch := make(chan delivery, 16)
	c := startTestConsumer(t, fakeGCEvents(t, dir, filepath.Join(dir, "capture.txt"), "hang"), city, filepath.Join(dir, "cursor.json"), ch)
	collect(t, ch, 1) // stream is live (mid-sleep) when Close hits it
	c.Close()         // must kill the hanging subprocess and return; the test binary's own timeout is the failure mode

	var nilC *Consumer
	nilC.Close() // nil-safe like the Emitter
}

func TestStartConsumerValidatesLoudly(t *testing.T) {
	city := newTestCity(t)
	dir := t.TempDir()
	gc := fakeGCEvents(t, dir, filepath.Join(dir, "capture.txt"), "hang")
	cursor := filepath.Join(dir, "cursor.json")
	bcast := func(string, json.RawMessage) bool { return true }

	cases := []ConsumerConfig{
		{GCBin: "", CityPath: city, CursorPath: cursor, Broadcast: bcast},
		{GCBin: filepath.Join(dir, "no-such-gc"), CityPath: city, CursorPath: cursor, Broadcast: bcast},
		{GCBin: gc, CityPath: t.TempDir(), CursorPath: cursor, Broadcast: bcast}, // no city.toml
		{GCBin: gc, CityPath: city, CursorPath: "", Broadcast: bcast},
		{GCBin: gc, CityPath: city, CursorPath: cursor, Broadcast: nil},
	}
	for i, cfg := range cases {
		if _, err := StartConsumer(cfg); err == nil {
			t.Errorf("case %d: want error, got nil", i)
		}
	}
}

func TestConsumerCorruptCursorTailsFromHead(t *testing.T) {
	city := newTestCity(t)
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.txt")
	cursorPath := filepath.Join(dir, "cursor.json")
	if err := os.WriteFile(cursorPath, []byte("{corrupt"), 0o644); err != nil {
		t.Fatalf("seed corrupt cursor: %v", err)
	}
	writeFixture(t, dir, 1, busLine(5, "parlay.message", "p", `{"n":5}`))
	ch := make(chan delivery, 16)
	c := startTestConsumer(t, fakeGCEvents(t, dir, capture, "hang"), city, cursorPath, ch)
	defer c.Close()

	collect(t, ch, 1)
	// Corrupt cursor degrades to the no-cursor path: no --after argument.
	recs := readCapture(t, capture)
	if len(recs) == 0 || strings.Join(recs[0].args, " ") != "events --follow" {
		t.Fatalf("argv after corrupt cursor: want plain follow, got %v", recs)
	}
}

// waitForCursor polls (liveness wait, not a timing assertion) until the
// cursor file holds want — persistence happens after Broadcast returns, so
// a delivery arriving on the channel doesn't guarantee the write finished.
func waitForCursor(t *testing.T, path string, want uint64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			last = string(b)
			var cur busCursor
			if json.Unmarshal(b, &cur) == nil && cur.AfterSeq == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("cursor never reached %d (last contents: %q)", want, last)
}
