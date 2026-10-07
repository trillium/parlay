package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Reading the delivery trail, and GET /delivery ────────────────────────────
// The writer half is relay_delivery_test.go. These tests are about the reader's
// one governing rule: an absence must never be reported as a healthy value. A
// never-written ledger is not an empty one, an unknown count is not zero, and a
// limit that does not parse must not fail the read.

// TestReadDeliveryFiltersNarrowsAndLimits pins the reader's three narrowing
// behaviours independently: a corrupt line is skipped rather than fatal, an
// agent filter excludes other channels, and limit keeps the NEWEST entries.
func TestReadDeliveryFiltersNarrowsAndLimits(t *testing.T) {
	r := newTestRelay(t, "http://127.0.0.1:1")
	for _, e := range []deliveryEntry{
		{Event: deliverySpooled, Agent: "a", Msg: "1"},
		{Event: deliverySpooled, Agent: "b", Msg: "2"},
		{Event: deliverySpooled, Agent: "a", Msg: "3"},
	} {
		r.appendDelivery(e)
	}
	// A corrupt line in the middle of the trail must hide nothing.
	f, err := os.OpenFile(r.deliveryPath(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{not json}\n\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	r.appendDelivery(deliveryEntry{Event: deliverySpooled, Agent: "a", Msg: "4"})

	all, exists := r.readDelivery(10, "")
	if !exists {
		t.Fatal("ledger reported absent after 4 appends")
	}
	if len(all) != 4 {
		t.Fatalf("unfiltered entries = %+v, want 4 (corrupt line skipped)", all)
	}

	only := mustRead(t, r, 10, "a")
	if len(only) != 3 || only[0].Msg != "1" || only[2].Msg != "4" {
		t.Fatalf("filtered entries = %+v, want a's 1,3,4 in order", only)
	}

	tail := mustRead(t, r, 2, "")
	if len(tail) != 2 || tail[0].Msg != "3" || tail[1].Msg != "4" {
		t.Fatalf("limit=2 entries = %+v, want the newest two (3,4)", tail)
	}
}

// TestReadDeliveryOverLimitReturnsNewestNotOldest guards the ring buffer's
// wraparound: with total > limit the entries kept must be the last ones, in
// order, not the last-written `limit` slots in write order.
func TestReadDeliveryOverLimitReturnsNewestNotOldest(t *testing.T) {
	r := newTestRelay(t, "http://127.0.0.1:1")
	for _, id := range []string{"1", "2", "3", "4", "5", "6", "7"} {
		r.appendDelivery(deliveryEntry{Event: deliverySpooled, Agent: "a", Msg: id})
	}
	got := mustRead(t, r, 3, "")
	want := []string{"5", "6", "7"}
	if len(got) != 3 {
		t.Fatalf("entries = %+v, want 3", got)
	}
	for i, id := range want {
		if got[i].Msg != id {
			t.Errorf("entry %d = %q, want %q", i, got[i].Msg, id)
		}
	}
}

func mustRead(t *testing.T, r *relay, limit int, agent string) []deliveryEntry {
	t.Helper()
	entries, _ := r.readDelivery(limit, agent)
	return entries
}

// TestReadDeliveryMissingFileIsAbsentNotEmpty: the whole "degrade honestly"
// requirement in one assertion. Neither an error nor an empty list is an
// acceptable answer for a trail that was never written.
func TestReadDeliveryMissingFileIsAbsentNotEmpty(t *testing.T) {
	r := newTestRelay(t, "http://127.0.0.1:1")
	entries, exists := r.readDelivery(10, "")
	if exists {
		t.Error("readDelivery reported a ledger that does not exist")
	}
	if entries == nil {
		t.Error("nil entries — a consumer must be able to range over an empty slice")
	}
	if len(entries) != 0 {
		t.Errorf("entries = %+v, want none", entries)
	}
}

// TestSpoolLineCountDistinguishesUnknownFromEmpty: an unreadable or unscannable
// spool must report -1, never 0. A zero would read as "nothing was pending",
// which is a healthy-looking answer to a question that has no answer.
func TestSpoolLineCountDistinguishesUnknownFromEmpty(t *testing.T) {
	dir := t.TempDir()
	if n := spoolLineCount(filepath.Join(dir, "nope.chan")); n != 0 {
		t.Errorf("absent spool = %d, want 0", n)
	}

	empty := filepath.Join(dir, "empty.chan")
	if err := os.WriteFile(empty, []byte("not a chat msg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := spoolLineCount(empty); n != 0 {
		t.Errorf("spool with no CHAT_MSG lines = %d, want 0", n)
	}

	two := filepath.Join(dir, "two.chan")
	if err := os.WriteFile(two, []byte("CHAT_MSG|1|user|hi\nCHAT_MSG|2|agent|yo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := spoolLineCount(two); n != 2 {
		t.Errorf("two-message spool = %d, want 2", n)
	}

	// A single line over the scan budget: unknown, not zero.
	huge := filepath.Join(dir, "huge.chan")
	if err := os.WriteFile(huge, []byte("CHAT_MSG|3|user|"+strings.Repeat("x", spoolScanMaxLine+10)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := spoolLineCount(huge); n != -1 {
		t.Errorf("oversized-line spool = %d, want -1 (unknown)", n)
	}

	if adir := filepath.Join(dir, "adir.chan"); os.Mkdir(adir, 0o755) == nil {
		if n := spoolLineCount(adir); n != -1 {
			t.Errorf("unreadable spool = %d, want -1 (unknown)", n)
		}
	}
}

// TestDeliveryEndpointReportsTheTrail exercises the surface an operator actually
// calls, including the fields that name its own degradation.
func TestDeliveryEndpointReportsTheTrail(t *testing.T) {
	r, _ := newDeliveryRelay(t)
	base, closeSrv := controlServer(t, r)
	defer closeSrv()

	// Nothing written yet: absent, not empty.
	var absent deliveryResponse
	getJSONInto(t, base+"/delivery", &absent)
	if !absent.OK || !absent.Enabled || absent.Exists || absent.Count != 0 {
		t.Fatalf("empty-state /delivery = %+v, want ok+enabled+absent+0", absent)
	}
	if absent.Ledger != r.deliveryPath() {
		t.Errorf("ledger = %q, want %q", absent.Ledger, r.deliveryPath())
	}
	if absent.Entries == nil {
		t.Error("entries is null — a consumer must be able to range over it")
	}

	r.appendDelivery(deliveryEntry{Event: deliverySpooled, Agent: "a", Msg: "1"})
	r.appendDelivery(deliveryEntry{Event: deliverySpooled, Agent: "b", Msg: "2"})

	var all deliveryResponse
	getJSONInto(t, base+"/delivery", &all)
	if !all.Exists || all.Count != 2 || len(all.Entries) != 2 {
		t.Fatalf("/delivery = %+v, want 2 entries", all)
	}

	var filtered deliveryResponse
	getJSONInto(t, base+"/delivery?agent=b", &filtered)
	if filtered.Count != 1 || filtered.Entries[0].Agent != "b" {
		t.Fatalf("?agent=b = %+v, want only b's entry", filtered)
	}

	// Garbage limit falls back to the default rather than failing the read.
	var garbage deliveryResponse
	getJSONInto(t, base+"/delivery?limit=nonsense", &garbage)
	if !garbage.OK || garbage.Count != 2 {
		t.Fatalf("?limit=nonsense = %+v, want the default read", garbage)
	}

	resp, err := http.Post(base+"/delivery", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /delivery = %d, want 405 (read-only surface)", resp.StatusCode)
	}
}

func getJSONInto(t *testing.T, url string, into any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("GET %s: decode %q: %v", url, body, err)
	}
}
