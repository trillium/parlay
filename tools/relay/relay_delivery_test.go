package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── Delivery ledger (relay_delivery.go) ──────────────────────────────────────
// The relay is the only component that knows WHEN a message reached an agent,
// and before this it recorded nothing about delivery at all: the spool format
// has no timestamp and cannot be widened (the monitor's line reader and
// lastSpooledID's resume cursor both depend on it). These tests pin the three
// things that make the trail worth reading — that it records delivery, that it
// records the failures too, and that it can never cost a delivery.

// newDeliveryRelay is a real relay against a temp runtime dir plus the fake
// upstream that will serve its messages.
func newDeliveryRelay(t *testing.T) (*relay, *fakeUpstream) {
	t.Helper()
	t.Setenv("PARLAY_RELAY_DELIVERY_LOG", "")
	up := newFakeUpstream()
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(srv.Close)
	r := newTestRelay(t, srv.URL)
	// Stop the poll loops before the fake server goes away, or every later log
	// line in the package is a retry against a closed port.
	t.Cleanup(func() { r.shutdown(&http.Server{}) })
	return r, up
}

// deliveryEntries reads the whole trail and fails if the file was never
// written at all — the state that must be distinguishable from an empty one.
func deliveryEntries(t *testing.T, r *relay) []deliveryEntry {
	t.Helper()
	entries, exists := r.readDelivery(maxDeliveryLimit, "")
	if !exists {
		t.Fatalf("delivery ledger %s does not exist", r.deliveryPath())
	}
	return entries
}

// waitForDelivery polls the trail until it holds at least want recorded
// entries, because the poll loop writes them from its own goroutine.
func waitForDelivery(t *testing.T, r *relay, want int) []deliveryEntry {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, _ := r.readDelivery(maxDeliveryLimit, "")
		if len(entries) >= want {
			return entries
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d ledger entries (have %d: %+v)", want, len(entries), entries)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func deliveryEvents(entries []deliveryEntry, event string) []deliveryEntry {
	var out []deliveryEntry
	for _, e := range entries {
		if e.Event == event {
			out = append(out, e)
		}
	}
	return out
}

// TestDeliveryLedgerRecordsWhatWasSpooledForWhomAndWhen is the whole point of
// the ledger: a per-message, timestamped, attributed record of the relay's own
// delivery boundary — with the message BODY deliberately absent, so the trail
// cannot become a second copy of user data.
func TestDeliveryLedgerRecordsWhatWasSpooledForWhomAndWhen(t *testing.T) {
	const body = "SENTINEL-BODY-MUST-NOT-BE-STORED"
	r, up := newDeliveryRelay(t)
	spool, err := r.register("crew-ledger")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer r.unregister("crew-ledger")

	up.setScript("crew-ledger", []upstreamMessage{
		{ID: "m-1", Role: "user", Text: body},
		{ID: "m-2", Role: "agent", Text: "ack", From: "other-agent"},
	})
	waitForSpoolCount(t, spool, 2)

	entries := waitForDelivery(t, r, 2)
	if len(entries) != 2 {
		t.Fatalf("ledger holds %d entries for 2 delivered messages: %+v", len(entries), entries)
	}
	want := []struct{ msg, role, from string }{
		{"m-1", "user", ""},
		{"m-2", "agent", "other-agent"},
	}
	for i, w := range want {
		e := entries[i]
		if e.Event != deliverySpooled || e.Agent != "crew-ledger" {
			t.Errorf("entry %d = %+v, want event=%s agent=crew-ledger", i, e, deliverySpooled)
		}
		if e.Msg != w.msg || e.Role != w.role || e.From != w.from {
			t.Errorf("entry %d = %+v, want msg=%s role=%s from=%s", i, e, w.msg, w.role, w.from)
		}
		if _, err := time.Parse(time.RFC3339, e.Ts); err != nil {
			t.Errorf("entry %d has no usable timestamp: ts=%q (%v)", i, e.Ts, err)
		}
	}

	// Identifiers and a clock only: the body must not be anywhere in the file.
	raw, err := os.ReadFile(r.deliveryPath())
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if strings.Contains(string(raw), body) {
		t.Error("delivery ledger stored a message body — it must record identifiers only")
	}

	// And the ledger must not have changed the spool contract it sits beside.
	if got := readSpoolIDs(t, spool); len(got) != 2 || got[0] != "m-1" || got[1] != "m-2" {
		t.Errorf("spool ids = %v, want [m-1 m-2]", got)
	}
	if id := lastSpooledID(spool); id != "m-2" {
		t.Errorf("lastSpooledID = %q, want m-2 (spool format must be untouched)", id)
	}
}

// TestDeliveryLedgerUnwritableDoesNotBlockDelivery is the load-bearing safety
// property: a ledger that cannot be written must be invisible to delivery. The
// spool path is made unwritable too, so the failure mode under test is exactly
// "observability is broken, does the message still arrive?"
func TestDeliveryLedgerUnwritableDoesNotBlockDelivery(t *testing.T) {
	r, up := newDeliveryRelay(t)
	// A directory where the ledger file should be: every O_APPEND open fails.
	if err := os.Mkdir(r.deliveryPath(), 0o755); err != nil {
		t.Fatalf("seed unwritable ledger: %v", err)
	}

	spool, err := r.register("crew-blk")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	up.setScript("crew-blk", []upstreamMessage{{ID: "b-1", Role: "user", Text: "still delivered"}})

	ids := waitForSpoolCount(t, spool, 1)
	if len(ids) != 1 || ids[0] != "b-1" {
		t.Fatalf("a broken ledger cost a delivery: spool ids = %v", ids)
	}
	if _, exists := r.readDelivery(10, ""); exists {
		t.Error("readDelivery reported a ledger that was never writable")
	}
}

// TestDeliveryLedgerDisabledSaysSoRatherThanLookingEmpty: with recording off
// there is no trail, and the reader must be able to tell that apart from a
// fleet that delivered nothing.
func TestDeliveryLedgerDisabledSaysSoRatherThanLookingEmpty(t *testing.T) {
	t.Setenv("PARLAY_RELAY_DELIVERY_LOG", "0")
	up := newFakeUpstream()
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()
	r := newTestRelay(t, srv.URL)

	spool, err := r.register("crew-off")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer r.unregister("crew-off")
	up.setScript("crew-off", []upstreamMessage{{ID: "o-1", Role: "user", Text: "x"}})
	waitForSpoolCount(t, spool, 1)

	entries, exists := r.readDelivery(10, "")
	if exists || len(entries) != 0 {
		t.Fatalf("disabled ledger: entries=%+v exists=%v, want empty and absent", entries, exists)
	}
}

// TestDeliveryLedgerRecordsSpoolFailure: a message the relay could NOT write is
// the exact case a "sent" record would lie about. It must be recorded as a
// failure, and the poll loop must keep running.
func TestDeliveryLedgerRecordsSpoolFailure(t *testing.T) {
	r, up := newDeliveryRelay(t)
	spool, err := r.register("crew-fail")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// Swap the spool file for a directory so the O_APPEND open fails, then let
	// the upstream produce a message.
	if err := os.Remove(spool); err != nil {
		t.Fatalf("remove spool: %v", err)
	}
	if err := os.Mkdir(spool, 0o755); err != nil {
		t.Fatalf("seed spool directory: %v", err)
	}
	up.setScript("crew-fail", []upstreamMessage{{ID: "f-1", Role: "user", Text: "lands nowhere"}})

	entries := waitForDelivery(t, r, 1)
	if len(entries) != 1 {
		t.Fatalf("ledger = %+v, want exactly one spool-failed entry", entries)
	}
	if entries[0].Event != deliverySpoolFailed || entries[0].Msg != "f-1" || entries[0].Agent != "crew-fail" {
		t.Fatalf("entry = %+v, want event=%s agent=crew-fail msg=f-1", entries[0], deliverySpoolFailed)
	}
	// The loop must still be alive and polling: one failed write is not terminal.
	before := up.pollCount("crew-fail")
	time.Sleep(120 * time.Millisecond)
	if after := up.pollCount("crew-fail"); after <= before {
		t.Error("a failed spool write stopped the poll loop")
	}
}

// TestDeliveryLedgerRecordsDeliveryEndedOnUnregister: retirement is a delivery
// fact — how much was sitting in the spool when the channel stopped — and the
// count must be taken before the spool is tombstoned, or it reads zero.
func TestDeliveryLedgerRecordsDeliveryEndedOnUnregister(t *testing.T) {
	r, up := newDeliveryRelay(t)
	spool, err := r.register("crew-retire")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	up.setScript("crew-retire", []upstreamMessage{
		{ID: "r-1", Role: "user", Text: "one"},
		{ID: "r-2", Role: "user", Text: "two"},
	})
	waitForSpoolCount(t, spool, 2)
	waitForDelivery(t, r, 2)

	r.unregister("crew-retire")

	ended := deliveryEvents(deliveryEntries(t, r), deliveryEnded)
	if len(ended) != 1 {
		t.Fatalf("delivery-ended entries = %+v, want exactly 1", ended)
	}
	e := ended[0]
	if e.Agent != "crew-retire" || e.Reason != reasonUnregister {
		t.Errorf("delivery-ended = %+v, want agent=crew-retire reason=%s", e, reasonUnregister)
	}
	if e.SpoolLines == nil || *e.SpoolLines != 2 {
		t.Errorf("delivery-ended spoolLines = %v, want 2 (counted before the tombstone)", e.SpoolLines)
	}
}

// TestDeliveryLedgerRecordsZeroSpoolLines is why SpoolLines is a pointer:
// "nothing was waiting" is a real answer, and omitempty would have turned it
// into a missing field that reads as unknown.
func TestDeliveryLedgerRecordsZeroSpoolLines(t *testing.T) {
	r, _ := newDeliveryRelay(t)
	spool, err := r.register("crew-empty")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	r.unregister("crew-empty")

	ended := deliveryEvents(deliveryEntries(t, r), deliveryEnded)
	if len(ended) != 1 {
		t.Fatalf("delivery-ended entries = %+v, want exactly 1", ended)
	}
	if ended[0].SpoolLines == nil {
		t.Fatalf("spoolLines was omitted for an empty spool: %+v", ended[0])
	}
	if *ended[0].SpoolLines != 0 {
		t.Errorf("spoolLines = %d, want 0", *ended[0].SpoolLines)
	}
	if _, err := os.Stat(spool + tombstoneSuffix); err != nil {
		t.Errorf("spool was not tombstoned: %v", err)
	}
}

// TestDeliveryLedgerRecordsChannelGoneWithPendingWork: the 410 path is the one
// where work is genuinely stranded — the relay stops polling a channel that
// still has unread messages in it. That count is the operator's answer to "how
// much did we lose when this channel died", and it has to be taken before the
// spool is tombstoned or it reads zero. This test asserts on the LEDGER rather
// than the spool for exactly that reason: on the 410 path the spool is renamed
// away microseconds after the message lands in it.
func TestDeliveryLedgerRecordsChannelGoneWithPendingWork(t *testing.T) {
	up := &oneThenGoneUpstream{msg: upstreamMessage{ID: "g-1", Role: "user", Text: "stranded"}}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()
	r := newTestRelay(t, srv.URL)

	spool, err := r.register("crew-gone")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// Stop the loop before this test returns: an observability writer must not
	// outlive the test that armed it.
	t.Cleanup(func() { r.shutdown(&http.Server{}) })
	waitForLoopGone(t, r, "crew-gone")
	if _, err := os.Stat(spool + tombstoneSuffix); err != nil {
		t.Fatalf("spool was not tombstoned after the 410: %v", err)
	}

	entries := waitForDelivery(t, r, 2)
	spooled := deliveryEvents(entries, deliverySpooled)
	if len(spooled) != 1 || spooled[0].Msg != "g-1" || spooled[0].Agent != "crew-gone" {
		t.Fatalf("spooled entries = %+v, want the one message the 410 stranded", spooled)
	}
	ended := deliveryEvents(entries, deliveryEnded)
	if len(ended) != 1 {
		t.Fatalf("delivery-ended entries = %+v, want exactly 1", ended)
	}
	if ended[0].Reason != reasonChannelGone {
		t.Errorf("reason = %q, want %q", ended[0].Reason, reasonChannelGone)
	}
	if ended[0].SpoolLines == nil || *ended[0].SpoolLines != 1 {
		t.Errorf("spoolLines = %v, want 1 (the message stranded by the 410, counted before the tombstone)", ended[0].SpoolLines)
	}
}

// TestDeliveryLedgerRecordsShutdown: a quiet window has two very different
// explanations — nobody sent anything, or the relay was off. The second is
// only visible if shutdown writes it.
func TestDeliveryLedgerRecordsShutdown(t *testing.T) {
	up := newFakeUpstream()
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	defer srv.Close()
	r := newTestRelay(t, srv.URL)
	if _, err := r.register("crew-down"); err != nil {
		t.Fatalf("register: %v", err)
	}

	r.shutdown(&http.Server{})

	ended := deliveryEvents(deliveryEntries(t, r), deliveryEnded)
	if len(ended) != 1 || ended[0].Reason != reasonShutdown {
		t.Fatalf("delivery-ended entries = %+v, want one with reason=%s", ended, reasonShutdown)
	}
}

// oneThenGoneUpstream serves one message for the first poll of a channel, then
// answers 410 — the shape that strands queued work.
type oneThenGoneUpstream struct {
	msg upstreamMessage
	mu  sync.Mutex
	hit bool
}

func (u *oneThenGoneUpstream) handler(w http.ResponseWriter, req *http.Request) {
	u.mu.Lock()
	first := !u.hit
	u.hit = true
	u.mu.Unlock()
	if first && req.URL.Query().Get("after") == "" {
		writeJSON(w, http.StatusOK, u.msg)
		return
	}
	writeJSON(w, http.StatusGone, map[string]any{"error": "channel gone", "gone": true})
}

func TestDeliveryLedgerRotatesAtTheCapAndSaysSo(t *testing.T) {
	r := newTestRelay(t, "http://127.0.0.1:1")
	r.deliveryMaxBytes = 1

	for i := 0; i < 3; i++ {
		r.appendDelivery(deliveryEntry{Event: deliverySpooled, Agent: "a", Msg: "m"})
	}

	if _, err := os.Stat(r.deliveryPath() + deliveryRotatedSuffix); err != nil {
		t.Fatalf("no previous generation at %s: %v", r.deliveryPath()+deliveryRotatedSuffix, err)
	}
	markers := deliveryEvents(deliveryEntries(t, r), deliveryRotated)
	if len(markers) == 0 {
		t.Fatal("rotation happened without leaving a rotated marker")
	}
	if markers[len(markers)-1].Reason != "size-cap" {
		t.Errorf("marker reason = %q, want size-cap", markers[len(markers)-1].Reason)
	}
}
