// Tests for the delivery half of `parlay explain`: the trail read off DISK
// when the relay does not answer.
//
// This is the state an operator is usually in when they run `parlay explain`
// — the relay is often the dead thing — so the trail it already wrote must
// still be shown, and the output must name the substitution. Every test here
// fails against the socket-only reader: it printed "delivery unknown — the
// relay did not answer" for a ledger that was on disk the whole time.
package commands

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/relayctl"
)

// ledgerFile writes one generation of the relay's delivery ledger in exactly
// the JSONL shape tools/relay/relay_delivery.go appends.
func ledgerFile(t *testing.T, path string, entries ...relayctl.DeliveryEntry) {
	t.Helper()
	var b strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal ledger entry: %v", err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func (f *explainFixture) ledger(t *testing.T, entries ...relayctl.DeliveryEntry) {
	t.Helper()
	ledgerFile(t, relayctl.LedgerPath(), entries...)
}

func (f *explainFixture) ledgerRotated(t *testing.T, entries ...relayctl.DeliveryEntry) {
	t.Helper()
	ledgerFile(t, relayctl.LedgerPathRotated(), entries...)
}

// serverHalf is the part of the fixture that works with no relay at all: the
// chat server answers, so registration and the command registry are real.
func (f *explainFixture) serverHalf(t *testing.T) {
	t.Helper()
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": subscribersBody("crew-1", "Crew One", "#abc", time.Now().UTC().Format(time.RFC3339)),
		"/api/chat/commands":    commandsBody(),
	})
}

// TestExplainReadsTheLedgerFromDiskWhenTheRelayIsDown is the point of the
// change: the relay is dead, and the trail it wrote is still on the screen —
// in the ledger's own vocabulary, filtered to this agent, and feeding the
// last-error line — with the substitution named on the line itself.
func TestExplainReadsTheLedgerFromDiskWhenTheRelayIsDown(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverHalf(t)
	t0 := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	t1 := time.Now().Add(-90 * time.Minute).UTC().Truncate(time.Second).Format(time.RFC3339)
	t2 := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	spoolLines := 2
	f.ledger(t,
		relayctl.DeliveryEntry{Ts: t0, Event: "spooled", Agent: "crew-1", Msg: "m-1", Role: "user", From: "captain"},
		relayctl.DeliveryEntry{Ts: t1, Event: "spool-failed", Agent: "crew-1", Msg: "m-9"},
		relayctl.DeliveryEntry{Ts: t1, Event: "spooled", Agent: "other", Msg: "m-7", Role: "agent"},
		relayctl.DeliveryEntry{Ts: t2, Event: "delivery-ended", Agent: "crew-1", Reason: "channel-gone", SpoolLines: &spoolLines},
	)
	// No relay started: no socket in this runtime dir.

	out, _, code, exited := explainRun(t, []string{"crew-1"})
	if exited {
		t.Fatalf("exited %d; the ledger on disk plus the server answered, so this is not a nothing-observable run", code)
	}
	wantLine(t, out,
		"delivery        read from disk ("+relayctl.LedgerPath()+") because the relay did not answer",
		"3 of the last 20 ledger event(s), oldest first;",
		"spooled msg m-1 role=user from=captain",
		"SPOOL FAILED for msg m-9 — it did not reach the agent",
		"delivery ended — reason=channel-gone spoolLines=2",
		"whether recording is switched off right now is unknown",
		// The disk rows are real evidence, so they feed the error line too.
		"last error      relay could not spool message m-9",
	)
	// Another agent's traffic is not this agent's story, and the socket-only
	// wording must be gone.
	notWantLine(t, out, "msg m-7", "delivery        unknown", "is not observable from here")
	if !(strings.Index(out, "msg m-1") < strings.Index(out, "msg m-9")) {
		t.Errorf("ledger rows are not in the file's own order (oldest first)\n---\n%s", out)
	}
}

// TestExplainDiskLedgerRotationIsNamed: the rotated generation is real
// evidence, so it is read and shown; the marker and the coverage note are what
// keep a short trail from reading as a quiet one.
func TestExplainDiskLedgerRotationIsNamed(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverHalf(t)
	t0 := time.Now().Add(-4 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	t1 := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	t2 := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	f.ledgerRotated(t, relayctl.DeliveryEntry{Ts: t0, Event: "spooled", Agent: "crew-1", Msg: "m-old"})
	f.ledger(t, relayctl.DeliveryEntry{Ts: t1, Event: "rotated", Reason: "size-cap"})
	// A line the relay never wrote: counted, skipped, never fatal.
	appendRawLedgerLine(t, relayctl.LedgerPath(), "{not json at all")
	appendRawLedgerLine(t, relayctl.LedgerPath(), mustJSON(t, relayctl.DeliveryEntry{Ts: t2, Event: "spooled", Agent: "crew-1", Msg: "m-new"}))

	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out,
		"spooled msg m-old",
		"ledger rotated (size-cap) — history before this line lives in delivery.log.1",
		"spooled msg m-new",
		"1 of them from the generation before the last rotation (older history is in "+relayctl.LedgerPathRotated()+")",
		"1 corrupt line(s) skipped",
	)
	if strings.Index(out, "msg m-old") > strings.Index(out, "msg m-new") {
		t.Errorf("the rotated generation must be printed oldest-first\n---\n%s", out)
	}
}

// TestExplainDiskLedgerUnreadableIsUnknownNotEmpty: a file that exists but
// cannot be opened is not an empty trail, and it is not a quiet fleet either.
func TestExplainDiskLedgerUnreadableIsUnknownNotEmpty(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions, so this mode cannot be simulated")
	}
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverHalf(t)
	f.ledger(t, relayctl.DeliveryEntry{Ts: time.Now().UTC().Format(time.RFC3339), Event: "spooled", Agent: "crew-1", Msg: "m-1"})
	if err := os.Chmod(relayctl.LedgerPath(), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(relayctl.LedgerPath(), 0o600) })

	out, _, code, exited := explainRun(t, []string{"crew-1"})
	if exited {
		t.Fatalf("exited %d; the server answered and the ledger is on disk (unreadably)", code)
	}
	wantLine(t, out,
		"because the relay did not answer — its ledger at "+relayctl.LedgerPath()+" exists but could not be read",
		"what it holds is unknown, not empty",
	)
	notWantLine(t, out, "no ledger on disk", "no events for this agent", "msg m-1")
}

// TestExplainDiskLedgerWithNoEventsForThisAgent: the file answered and holds
// nothing for this agent. That is an ANSWER — not the "nothing was
// observable" exit — and it must not be dressed up as a delivery history.
func TestExplainDiskLedgerWithNoEventsForThisAgent(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.deadServer(t)
	f.ledger(t, relayctl.DeliveryEntry{Ts: time.Now().UTC().Format(time.RFC3339), Event: "spooled", Agent: "other", Msg: "m-7"})

	out, _, code, exited := explainRun(t, []string{"crew-1"})
	if exited {
		t.Fatalf("exited %d; the ledger on disk was read, so something was observable", code)
	}
	wantLine(t, out,
		"read from disk ("+relayctl.LedgerPath()+") because the relay did not answer — ledger present, no events for this agent",
	)
	notWantLine(t, out, "msg m-7", "no ledger on disk")
}

// TestExplainSocketAnswerBeatsAStaleLedgerOnDisk is the guard against the
// obvious over-reach: a live relay's answer is authoritative even when an
// older ledger happens to sit in the same runtime dir, so the disk path must
// not be taken (and must not be announced) while the socket answers.
func TestExplainSocketAnswerBeatsAStaleLedgerOnDisk(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverHalf(t)
	f.ledger(t, relayctl.DeliveryEntry{Ts: "2020-01-01T00:00:00Z", Event: "spooled", Agent: "crew-1", Msg: "stale-on-disk"})
	f.relay(t, relayctl.Health{OK: true, Server: f.server, Runtime: f.runtime}, []string{"crew-1"}, &relayctl.Delivery{
		OK: true, Enabled: true, Exists: true, Ledger: relayctl.LedgerPath(), Count: 1,
		Entries: []relayctl.DeliveryEntry{{Ts: time.Now().UTC().Format(time.RFC3339), Event: "spooled", Agent: "crew-1", Msg: "from-the-live-relay"}},
	})

	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out, "delivery        1 of the last 20 ledger event(s), oldest first:", "spooled msg from-the-live-relay")
	notWantLine(t, out, "read from disk", "stale-on-disk", "whether recording is switched off right now is unknown")
	if !f.relaySawOnlyGets() {
		t.Errorf("explain made a non-GET request to the relay: %v", f.relayMethods)
	}
}

// TestExplainDiskLedgerNeverRelabelsSpooledAsDelivered: the disk path gets the
// same vocabulary as the socket path. Reading a file does not make the relay
// any more able to see the read that nothing in this fleet acknowledges.
func TestExplainDiskLedgerNeverRelabelsSpooledAsDelivered(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverHalf(t)
	f.ledger(t, relayctl.DeliveryEntry{Ts: time.Now().UTC().Format(time.RFC3339), Event: "spooled", Agent: "crew-1", Msg: "m-1", Role: "user"})
	f.spool(t, "CHAT_MSG|m-1|user|hello")

	out, _, _, _ := explainRun(t, []string{"crew-1"})
	wantLine(t, out, "spooled msg m-1 role=user", "unconfirmed-consumed")
	notWantLine(t, out, "delivered", "delivery of msg m-1")
}

// TestExplainDiskCoverageText pins the coverage caveats that a synthetic trail
// cannot produce without a 16 MiB file: truncation and corruption are named
// rather than silently shortening history.
func TestExplainDiskCoverageText(t *testing.T) {
	for _, tc := range []struct {
		name string
		view explainDelivery
		want []string
		not  []string
	}{
		{
			name: "complete",
			view: explainDelivery{Path: "/rt/delivery.log", Exists: true},
			not:  []string{"rotation", "TRUNCATED", "corrupt"},
		},
		{
			name: "truncated",
			view: explainDelivery{Path: "/rt/delivery.log", Exists: true, Truncated: true},
			want: []string{"TRUNCATED: the file exceeded this reader's cap"},
		},
		{
			name: "rotated and corrupt",
			view: explainDelivery{Path: "/rt/delivery.log", Exists: true, FromRotated: 2, Corrupt: 3},
			want: []string{"2 of them from the generation before the last rotation", "3 corrupt line(s) skipped"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.view.coverage()
			wantLine(t, got, tc.want...)
			notWantLine(t, got, tc.not...)
		})
	}
}

// TestExplainDiskLedgerHalfUnreadableStillShowsWhatWasRead: one generation
// refusing to open must not hide the rows the OTHER one answered with. The
// "contents unknown" line is for a trail with nothing readable at all, and
// whichever generation failed is named.
func TestExplainDiskLedgerHalfUnreadableStillShowsWhatWasRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions, so this mode cannot be simulated")
	}
	oldStamp := time.Now().Add(-5 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	newStamp := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)

	t.Run("the active generation is unreadable", func(t *testing.T) {
		noSleep(t)
		f := newExplainFixture(t, "crew-1")
		f.serverHalf(t)
		f.ledgerRotated(t, relayctl.DeliveryEntry{Ts: oldStamp, Event: "spooled", Agent: "crew-1", Msg: "m-old"})
		f.ledger(t, relayctl.DeliveryEntry{Ts: newStamp, Event: "spooled", Agent: "crew-1", Msg: "m-new"})
		if err := os.Chmod(relayctl.LedgerPath(), 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(relayctl.LedgerPath(), 0o600) })

		out, _, code, exited := explainRun(t, []string{"crew-1"})
		if exited {
			t.Fatalf("exited %d; the rotated generation was read", code)
		}
		wantLine(t, out,
			"spooled msg m-old",
			"the active ledger could not be read in full",
			"so this may be short",
		)
		notWantLine(t, out, "msg m-new", "what it holds is unknown, not empty")
	})

	t.Run("the rotated generation is unreadable", func(t *testing.T) {
		noSleep(t)
		f := newExplainFixture(t, "crew-1")
		f.serverHalf(t)
		f.ledgerRotated(t, relayctl.DeliveryEntry{Ts: oldStamp, Event: "spooled", Agent: "crew-1", Msg: "m-old"})
		f.ledger(t, relayctl.DeliveryEntry{Ts: newStamp, Event: "spooled", Agent: "crew-1", Msg: "m-new"})
		if err := os.Chmod(relayctl.LedgerPathRotated(), 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(relayctl.LedgerPathRotated(), 0o600) })

		out, _, code, exited := explainRun(t, []string{"crew-1"})
		if exited {
			t.Fatalf("exited %d; the active generation was read", code)
		}
		wantLine(t, out,
			"spooled msg m-new",
			"the previous generation ("+relayctl.LedgerPathRotated()+") could not be read",
			"so older history is unknown",
		)
		notWantLine(t, out, "msg m-old", "what it holds is unknown, not empty")
	})
}

func mustJSON(t *testing.T, e relayctl.DeliveryEntry) string {
	t.Helper()
	line, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(line)
}

func appendRawLedgerLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("append to %s: %v", path, err)
	}
}
