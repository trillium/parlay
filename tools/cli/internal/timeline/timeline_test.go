package timeline

import (
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/relayctl"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

func spooled(ts, agent, msg, role string) DeliveryRecord {
	return DeliveryRecord{Entry: relayctl.DeliveryEntry{Ts: ts, Event: "spooled", Agent: agent, Msg: msg, Role: role}}
}

func onLine(agent, msg string) map[string]Presence {
	return map[string]Presence{agent: {Known: true, IDs: map[string]int{msg: 1}}}
}

func find(t *testing.T, events []Event, msg string) Event {
	t.Helper()
	for _, e := range events {
		if e.Msg == msg {
			return e
		}
	}
	t.Fatalf("no event for msg %q in %d event(s)", msg, len(events))
	return Event{}
}

// TestSpooledStillInSpoolIsQueuedNotDelivered pins the outcome the whole
// package exists to get right: a line the relay handed over and that is STILL
// sitting in the spool is queued, and the word delivered never appears.
func TestSpooledStillInSpoolIsQueuedNotDelivered(t *testing.T) {
	recs := Records{Delivery: []DeliveryRecord{spooled("2026-10-07T08:00:00Z", "crew-1", "m-1", "user")}}
	events := Build(recs, onLine("crew-1", "m-1"))

	got := find(t, events, "m-1")
	if got.Outcome != OutcomeQueued {
		t.Fatalf("outcome = %q, want %q", got.Outcome, OutcomeQueued)
	}
	if !strings.Contains(got.Detail, "queued, not delivered") {
		t.Errorf("a queued line must carry the explicit 'not delivered' caveat: %q", got.Detail)
	}
	if !strings.Contains(got.Detail, "still in") {
		t.Errorf("queued detail should say the line is still in the spool: %q", got.Detail)
	}
}

// TestSpooledLineGoneIsLeftSpoolNotDelivered is the other half of the same
// rule: the line's absence from the spool proves it left, and proves nothing
// about whether anyone read it.
func TestSpooledLineGoneIsLeftSpoolNotDelivered(t *testing.T) {
	recs := Records{Delivery: []DeliveryRecord{spooled("2026-10-07T08:00:00Z", "crew-1", "m-1", "user")}}
	events := Build(recs, map[string]Presence{"crew-1": {Known: true, IDs: map[string]int{}}})

	got := find(t, events, "m-1")
	if got.Outcome != OutcomeLeftSpool {
		t.Fatalf("outcome = %q, want %q", got.Outcome, OutcomeLeftSpool)
	}
	if !strings.Contains(got.Detail, "read or pruned") {
		t.Errorf("left-spool must admit it cannot tell read from pruned: %q", got.Detail)
	}
}

// TestUnreadableSpoolIsUnknownNotGone: absence of evidence must not become
// evidence of absence. An unreadable spool makes the state unknowable, which
// is a third answer, distinct from both queued and left-spool.
func TestUnreadableSpoolIsUnknownNotGone(t *testing.T) {
	recs := Records{Delivery: []DeliveryRecord{spooled("2026-10-07T08:00:00Z", "crew-1", "m-1", "user")}}
	events := Build(recs, map[string]Presence{"crew-1": {Reason: "unreadable (permission denied)"}})

	got := find(t, events, "m-1")
	if got.Outcome != OutcomeUnknown {
		t.Fatalf("outcome = %q, want %q", got.Outcome, OutcomeUnknown)
	}
	if !strings.Contains(got.Detail, "permission denied") {
		t.Errorf("an unknown outcome must carry the reason it is unknown: %q", got.Detail)
	}
}

// TestNoPresenceEntryAtAllIsUnknown covers the case where the caller never
// looked up the agent's spool (beyond the read cap, say): the zero Presence
// must read as unknown, never as "not in the spool".
func TestNoPresenceEntryAtAllIsUnknown(t *testing.T) {
	recs := Records{Delivery: []DeliveryRecord{spooled("2026-10-07T08:00:00Z", "crew-1", "m-1", "user")}}
	got := find(t, Build(recs, nil), "m-1")
	if got.Outcome != OutcomeUnknown {
		t.Fatalf("outcome = %q, want %q", got.Outcome, OutcomeUnknown)
	}
}

// TestRepeatedHandOverSupersedesTheEarlierOne: one message id handed over
// twice is one message. The newest hand-over is the live delivery; the earlier
// line is superseded and says what superseded it. This is the shape a relay
// restart produces when the resume cursor cannot be seeded from the spool.
func TestRepeatedHandOverSupersedesTheEarlierOne(t *testing.T) {
	recs := Records{Delivery: []DeliveryRecord{
		spooled("2026-10-07T08:00:00Z", "crew-1", "m-1", "user"),
		spooled("2026-10-07T08:03:00Z", "crew-1", "m-1", "user"),
	}}
	events := Build(recs, onLine("crew-1", "m-1"))
	if len(events) != 2 {
		t.Fatalf("got %d events, want both hand-overs recorded", len(events))
	}
	if events[0].Outcome != OutcomeSuperseded || events[1].Outcome != OutcomeQueued {
		t.Fatalf("outcomes = %q then %q, want superseded then queued", events[0].Outcome, events[1].Outcome)
	}
	if !strings.Contains(events[0].Detail, "2 time(s)") {
		t.Errorf("superseded detail should say how many hand-overs there were: %q", events[0].Detail)
	}
	if events[0].Msg != "m-1" {
		t.Errorf("a superseded event still names its message: %q", events[0].Msg)
	}
}

// TestSupersessionUsesReadOrderNotStamps: an append-only trail's read order is
// its write order, so a stamp that does not parse must not be able to reorder
// which hand-over counts as live.
func TestSupersessionUsesReadOrderNotStamps(t *testing.T) {
	recs := Records{Delivery: []DeliveryRecord{
		spooled("", "crew-1", "m-1", "user"),
		spooled("2026-10-07T08:00:00Z", "crew-1", "m-1", "user"),
	}}
	events := Build(recs, onLine("crew-1", "m-1"))
	// The last record read is the live one, whatever the stamps say.
	for _, e := range events {
		if e.HasAt && e.Outcome != OutcomeQueued {
			t.Errorf("stamped record outcome = %q, want queued", e.Outcome)
		}
		if !e.HasAt && e.Outcome != OutcomeSuperseded {
			t.Errorf("undated first record outcome = %q, want superseded", e.Outcome)
		}
	}
}

// TestSpoolFailureIsDroppedAndNeverSuperseded: a failed append is the one
// unambiguously bad outcome. Nothing in this data plane retries a spool write,
// so an earlier failure is not "superseded" by a later one.
func TestSpoolFailureIsDroppedAndNeverSuperseded(t *testing.T) {
	recs := Records{Delivery: []DeliveryRecord{
		{Entry: relayctl.DeliveryEntry{Ts: "2026-10-07T08:00:00Z", Event: "spool-failed", Agent: "crew-1", Msg: "m-1"}},
		{Entry: relayctl.DeliveryEntry{Ts: "2026-10-07T08:01:00Z", Event: "spool-failed", Agent: "crew-1", Msg: "m-1"}},
	}}
	events := Build(recs, onLine("crew-1", "m-1"))
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	for _, e := range events {
		if e.Outcome != OutcomeDropped {
			t.Errorf("outcome = %q, want dropped for every failed append", e.Outcome)
		}
	}
}

// TestDeliveryEndedCarriesReasonAndCount: the count of lines still waiting is
// a pointer on purpose, so "0 were waiting" and "not recorded" stay different.
func TestDeliveryEndedCarriesReasonAndCount(t *testing.T) {
	zero := 0
	recs := Records{Delivery: []DeliveryRecord{
		{Entry: relayctl.DeliveryEntry{Ts: "2026-10-07T08:00:00Z", Event: "delivery-ended", Agent: "crew-1", Reason: "channel-gone", SpoolLines: &zero}},
		{Entry: relayctl.DeliveryEntry{Ts: "2026-10-07T08:01:00Z", Event: "delivery-ended", Agent: "crew-2", Reason: "unregister"}},
	}}
	events := Build(recs, nil)
	if events[0].Outcome != OutcomeEnded || !strings.Contains(events[0].Detail, "0 line(s)") {
		t.Errorf("explicit zero spoolLines should read as 0, got %q", events[0].Detail)
	}
	if matches := strings.Count(events[1].Detail, "unrecorded number of lines"); matches != 1 {
		t.Errorf("missing spoolLines must read as unrecorded, not zero: %q", events[1].Detail)
	}
}

// TestUnknownEventShapeIsKeptAndNamed: a newer relay may write an event this
// reader predates. Dropping it would hide real evidence; classifying it would
// be an invention.
func TestUnknownEventShapeIsKeptAndNamed(t *testing.T) {
	recs := Records{Delivery: []DeliveryRecord{
		{Entry: relayctl.DeliveryEntry{Ts: "2026-10-07T08:00:00Z", Event: "throttled", Agent: "crew-1"}},
	}}
	got := find2(t, Build(recs, nil))
	if got.Outcome != OutcomeUnknown {
		t.Fatalf("outcome = %q, want unknown", got.Outcome)
	}
	if !strings.Contains(got.Detail, "throttled") {
		t.Errorf("the unclassified event name must appear: %q", got.Detail)
	}
}

func find2(t *testing.T, events []Event) Event {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	return events[0]
}

// TestRelayStartAndResumeAreTheirOwnOutcomes: the relay's own lifecycle is a
// fact only the relay witnesses, and it is the one that explains a gap in
// deliveries. Two rules are pinned here — `started` names no channel (it is the
// process, not a channel) and `resumed` claims POLLING, never a read.
func TestRelayStartAndResumeAreTheirOwnOutcomes(t *testing.T) {
	recs := Records{Delivery: []DeliveryRecord{
		{Entry: relayctl.DeliveryEntry{Ts: "2026-10-07T08:00:00Z", Event: "started"}},
		{Entry: relayctl.DeliveryEntry{Ts: "2026-10-07T08:00:01Z", Event: "resumed", Agent: "crew-1"}},
	}}
	events := Build(recs, nil)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	start, res := events[0], events[1]
	if start.Outcome != OutcomeStarted {
		t.Fatalf("started outcome = %q, want %q", start.Outcome, OutcomeStarted)
	}
	if start.Agent != "" {
		t.Errorf("a relay start names no channel, got agent %q — an invented agent would send an operator to the wrong one", start.Agent)
	}
	if !strings.Contains(start.Detail, "RESTART") {
		t.Errorf("the start row must say a gap is a restart, not a quiet fleet: %q", start.Detail)
	}

	if res.Outcome != OutcomeResumed || res.Agent != "crew-1" {
		t.Fatalf("resumed event = %+v, want a resumed outcome for crew-1", res)
	}
	if !strings.Contains(res.Detail, "POLL") {
		t.Errorf("resumed must claim polling: %q", res.Detail)
	}
	for _, forbidden := range []string{"delivered", "READ", "read it"} {
		if strings.Contains(res.Detail, forbidden) {
			t.Errorf("a resumed row must never claim a read (%q): %q", forbidden, res.Detail)
		}
	}
}

// TestLifecycleAndRefusalOutcomes maps the control plane onto the vocabulary.
func TestLifecycleAndRefusalOutcomes(t *testing.T) {
	recs := Records{Audit: []relayctl.AuditEntry{
		{Ts: "2026-10-07T07:00:00Z", Actor: "fp1", Action: "register", Agent: "crew-1"},
		{Ts: "2026-10-07T07:30:00Z", Actor: "none", Action: "register-denied", Agent: "crew-1"},
		{Ts: "2026-10-07T08:00:00Z", Actor: "fp1", Action: "unregister", Agent: "crew-1"},
		{Ts: "2026-10-07T08:01:00Z", Actor: "fp1", Action: "invented", Agent: "crew-1"},
	}}
	events := Build(recs, nil)
	want := []Outcome{OutcomeEnrolled, OutcomeRefused, OutcomeRetired, OutcomeUnknown}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d", len(events), len(want))
	}
	for i, w := range want {
		if events[i].Outcome != w {
			t.Errorf("event %d outcome = %q, want %q", i, events[i].Outcome, w)
		}
		if events[i].Agent != "crew-1" {
			t.Errorf("event %d lost its agent", i)
		}
	}
	if !strings.Contains(events[1].Detail, "another caller held the channel") {
		t.Errorf("a refusal should say why: %q", events[1].Detail)
	}
}

// TestCommandDetailCarriesOutcomeAndExitCode is answer #3's row: what the last
// command did and whether it succeeded.
func TestCommandDetailCarriesOutcomeAndExitCode(t *testing.T) {
	exit := 1
	recs := Records{Commands: []wire.CommandInvocation{
		{Verb: "send", Agent: "crew-1", State: "failed", StartedAt: "2026-10-07T08:00:00Z", ExitCode: &exit, Outcome: "error", DurationMs: 1234},
		{Verb: "listen", Agent: "crew-1", State: "running", StartedAt: "2026-10-07T08:00:00Z"},
	}}
	events := Build(recs, nil)
	if events[0].Outcome != OutcomeCommand || events[0].Source != SourceCommand {
		t.Fatalf("command event = %+v", events[0])
	}
	for _, frag := range []string{"send → failed", "exit=1", "outcome=error", "took=1s"} {
		if !strings.Contains(events[0].Detail, frag) {
			t.Errorf("command detail %q missing %q", events[0].Detail, frag)
		}
	}
	if !strings.Contains(events[1].Detail, "running-for=") {
		t.Errorf("a running command should report how long it has been running: %q", events[1].Detail)
	}
}

// TestUndatedEventKeepsItsRowAndSortsLast: the event is real evidence and the
// time is not known; both of those must survive into the output.
func TestUndatedEventKeepsItsRowAndSortsLast(t *testing.T) {
	recs := Records{
		Delivery: []DeliveryRecord{{Entry: relayctl.DeliveryEntry{Ts: "not-a-time", Event: "rotated", Reason: "size-cap"}}},
		Audit:    []relayctl.AuditEntry{{Ts: "2026-10-07T08:00:00Z", Action: "register", Agent: "crew-1"}},
	}
	events := Build(recs, nil)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (the undated one is not dropped)", len(events))
	}
	if !events[0].HasAt {
		t.Errorf("the undated event sorted first; it must sort after the timed ones")
	}
	if events[1].HasAt {
		t.Errorf("the undated event must sort last")
	}
	kept, _ := Select(events, Filter{Window: Window{Since: time.Now(), HasSince: true}}, 0)
	if len(kept) != 1 || kept[0].HasAt {
		t.Errorf("a window cannot date an undated event, so it is kept rather than dropped: %+v", kept)
	}
}

// TestSelectFiltersAndKeepsTheNewest exercises the three query axes together.
func TestSelectFiltersAndKeepsTheNewest(t *testing.T) {
	base := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	mk := func(off time.Duration, agent, msg string) DeliveryRecord {
		return spooled(base.Add(off).Format(time.RFC3339), agent, msg, "user")
	}
	recs := Records{Delivery: []DeliveryRecord{
		mk(0, "crew-1", "m-1"), mk(time.Minute, "crew-2", "m-2"),
		mk(2*time.Minute, "crew-1", "m-3"), mk(3*time.Minute, "crew-1", "m-4"),
	}}
	all := Build(recs, map[string]Presence{
		"crew-1": {Known: true, IDs: map[string]int{}},
		"crew-2": {Known: true, IDs: map[string]int{}},
	})

	kept, matched := Select(all, Filter{Agent: "crew-1"}, 2)
	if matched != 3 || len(kept) != 2 {
		t.Fatalf("matched/kept = %d/%d, want 3/2", matched, len(kept))
	}
	if kept[0].Msg != "m-3" || kept[1].Msg != "m-4" {
		t.Errorf("the NEWEST two must be kept in chronological order, got %s then %s", kept[0].Msg, kept[1].Msg)
	}

	kept, matched = Select(all, Filter{Window: Window{Since: base.Add(time.Minute), HasSince: true}}, 0)
	if matched != 3 {
		t.Errorf("window matched %d, want 3", matched)
	}

	kept, matched = Select(all, Filter{Outcomes: map[Outcome]bool{OutcomeLeftSpool: true}}, 0)
	if matched != 4 || len(kept) != 4 {
		t.Errorf("all four are left-spool; matched=%d len=%d", matched, len(kept))
	}
	kept, matched = Select(all, Filter{Outcomes: map[Outcome]bool{OutcomeDropped: true}}, 0)
	if matched != 0 || len(kept) != 0 {
		t.Errorf("nothing was dropped; matched=%d len=%d", matched, len(kept))
	}
	if kept == nil {
		t.Error("a nil slice would render as a missing field in --json; want an empty one")
	}
}

func TestParseOutcomeIsCaseInsensitiveAndClosed(t *testing.T) {
	if o, ok := ParseOutcome("DROPPED"); !ok || o != OutcomeDropped {
		t.Errorf("DROPPED did not resolve: %q %v", o, ok)
	}
	if _, ok := ParseOutcome("delivered"); ok {
		t.Error("`delivered` must not be an outcome — this fleet cannot prove a read")
	}
	for _, o := range Outcomes {
		if _, ok := ParseOutcome(string(o)); !ok {
			t.Errorf("%q is in Outcomes but does not parse", o)
		}
	}
}

func TestCountsFollowTheVocabularyOrder(t *testing.T) {
	events := []Event{{Outcome: OutcomeRotated}, {Outcome: OutcomeQueued}, {Outcome: OutcomeQueued}}
	got := strings.Join(Counts(events), " ")
	if got != "queued=2 rotated=1" {
		t.Errorf("Counts = %q, want queued before rotated (vocabulary order, stable across runs)", got)
	}
}
