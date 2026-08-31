package supersession

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func mustRegister(t *testing.T, l *Ledger, id, kind, name, version string) Record {
	t.Helper()
	v, err := ParseVersion(version)
	if err != nil {
		t.Fatalf("ParseVersion(%q): %v", version, err)
	}
	r := Record{ID: id, Kind: kind, Name: name, Version: v}
	if _, err := l.Register(Registration{Record: r, Actor: "test", At: "2026-08-30T00:00:00Z"}); err != nil {
		t.Fatalf("Register(%s): %v", id, err)
	}
	return r
}

func mustSupersede(t *testing.T, l *Ledger, id, targetID, version string, class ChangeClass, reason string) Record {
	t.Helper()
	target, ok := l.Record(targetID)
	if !ok {
		t.Fatalf("target %s not found", targetID)
	}
	v, err := ParseVersion(version)
	if err != nil {
		t.Fatalf("ParseVersion(%q): %v", version, err)
	}
	r := Record{ID: id, Kind: target.Kind, Name: target.Name, Version: v, Supersedes: targetID}
	_, err = l.Supersede(Supersession{
		Record:  r,
		Changes: []Change{{Class: class, Detail: "test change"}},
		Reason:  reason,
		Actor:   "test",
		At:      "2026-08-30T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("Supersede(%s): %v", id, err)
	}
	return r
}

func TestParseVersion(t *testing.T) {
	good := map[string]Version{
		"0.0.0":  {0, 0, 0},
		"1.2.3":  {1, 2, 3},
		"10.0.4": {10, 0, 4},
	}
	for in, want := range good {
		got, err := ParseVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v, nil", in, got, err, want)
		}
	}
	bad := []string{"", "1.2", "1.2.3.4", "v1.2.3", "1.2.-3", "1.2.3-rc1", "1..3", "1.2.x"}
	for _, in := range bad {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q): want error, got nil", in)
		}
	}
}

func TestBumpKind(t *testing.T) {
	cases := []struct {
		from, to string
		want     Severity
		wantErr  string
	}{
		{"1.2.3", "1.2.4", SeverityPatch, ""},
		{"1.2.3", "1.2.10", SeverityPatch, ""},
		{"1.2.3", "1.3.0", SeverityMinor, ""},
		{"1.2.3", "1.5.0", SeverityMinor, ""},
		{"1.2.3", "2.0.0", SeverityMajor, ""},
		{"1.2.3", "4.0.0", SeverityMajor, ""},
		{"1.2.3", "1.2.3", "", "must increase"},
		{"1.2.3", "1.2.2", "", "must increase"},
		{"1.2.3", "0.9.0", "", "must increase"},
		{"1.2.3", "2.1.0", "", "must reset minor and patch"},
		{"1.2.3", "2.0.1", "", "must reset minor and patch"},
		{"1.2.3", "1.3.1", "", "must reset patch"},
	}
	for _, c := range cases {
		from, _ := ParseVersion(c.from)
		to, _ := ParseVersion(c.to)
		got, err := BumpKind(from, to)
		if c.wantErr == "" {
			if err != nil || got != c.want {
				t.Errorf("BumpKind(%s, %s) = %q, %v; want %q, nil", c.from, c.to, got, err, c.want)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("BumpKind(%s, %s): want error containing %q, got %v", c.from, c.to, c.wantErr, err)
		}
	}
}

// The core #128 loop: supersede, then resolve the head; every version stays
// queryable, root first.
func TestSupersedeAndResolveHead(t *testing.T) {
	l := NewLedger()
	v1 := mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")

	head, ok := l.Head("triage")
	if !ok || head.ID != v1.ID {
		t.Fatalf("Head after register = %v, %v; want %s", head, ok, v1.ID)
	}

	v2 := mustSupersede(t, l, "wf-2", "wf-1", "1.1.0", ChangeAdditive, "added optional review stage")
	v3 := mustSupersede(t, l, "wf-3", "wf-2", "2.0.0", ChangeBreaking, "removed manual approval stage")

	head, ok = l.Head("triage")
	if !ok || head.ID != v3.ID {
		t.Fatalf("Head after two supersessions = %v, %v; want %s", head, ok, v3.ID)
	}

	// Every version remains queryable (#128 §15), in chain order.
	hist := l.History("triage")
	gotIDs := []string{}
	for _, r := range hist {
		gotIDs = append(gotIDs, r.ID)
	}
	if want := []string{v1.ID, v2.ID, v3.ID}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("History = %v; want %v", gotIDs, want)
	}

	// Superseded records still resolve by ID with their exact version —
	// existing work keeps its exact governing reference (#128 §14).
	got, ok := l.Record("wf-1")
	if !ok || got.Version.String() != "1.0.0" {
		t.Fatalf("Record(wf-1) = %v, %v; want version 1.0.0", got, ok)
	}
	if succ := l.SupersededBy("wf-1"); succ != "wf-2" {
		t.Fatalf("SupersededBy(wf-1) = %q; want wf-2", succ)
	}
	if succ := l.SupersededBy("wf-3"); succ != "" {
		t.Fatalf("SupersededBy(head) = %q; want empty", succ)
	}
}

func TestSupersedeStructuralRejections(t *testing.T) {
	setup := func() *Ledger {
		l := NewLedger()
		mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
		mustSupersede(t, l, "wf-2", "wf-1", "1.1.0", ChangeAdditive, "add stage")
		return l
	}
	v120, _ := ParseVersion("1.2.0")
	valid := func() Supersession {
		return Supersession{
			Record:  Record{ID: "wf-3", Kind: "workflow", Name: "triage", Version: v120, Supersedes: "wf-2"},
			Changes: []Change{{Class: ChangeAdditive, Detail: "d"}},
			Reason:  "r",
		}
	}

	cases := []struct {
		name    string
		mutate  func(*Supersession)
		wantErr string
	}{
		{"stale supersede of non-head", func(s *Supersession) { s.Record.Supersedes = "wf-1" }, "stale supersede"},
		{"missing target", func(s *Supersession) { s.Record.Supersedes = "wf-99" }, "does not exist"},
		{"no target", func(s *Supersession) { s.Record.Supersedes = "" }, "must name the record"},
		{"duplicate id", func(s *Supersession) { s.Record.ID = "wf-2" }, "already exists"},
		{"renamed chain", func(s *Supersession) { s.Record.Name = "other" }, "re-identify"},
		{"changed kind", func(s *Supersession) { s.Record.Kind = "contract" }, "does not match target kind"},
		{"version not increased", func(s *Supersession) { s.Record.Version = Version{1, 1, 0} }, "must increase"},
		{"empty changes", func(s *Supersession) { s.Changes = nil }, "must say what changed"},
		{"unknown change class", func(s *Supersession) { s.Changes = []Change{{Class: "vibes"}} }, "unknown class"},
		{"empty reason", func(s *Supersession) { s.Reason = "" }, "must say why"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := setup()
			s := valid()
			c.mutate(&s)
			if _, err := l.Supersede(s); err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("want error containing %q, got %v", c.wantErr, err)
			}
			// The rejection must leave state untouched: head unchanged.
			if head, _ := l.Head("triage"); head.ID != "wf-2" {
				t.Fatalf("rejected supersede moved the head to %s", head.ID)
			}
		})
	}

	// The valid supersession itself works against the same setup.
	l := setup()
	if _, err := l.Supersede(valid()); err != nil {
		t.Fatalf("valid supersession rejected: %v", err)
	}
}

func TestRegisterRejections(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
	v1, _ := ParseVersion("1.0.0")

	cases := []struct {
		name    string
		rec     Record
		wantErr string
	}{
		{"duplicate id", Record{ID: "wf-1", Kind: "workflow", Name: "other", Version: v1}, "already exists"},
		{"duplicate chain name", Record{ID: "wf-9", Kind: "workflow", Name: "triage", Version: v1}, "supersede it instead"},
		{"root that supersedes", Record{ID: "wf-9", Kind: "workflow", Name: "other", Version: v1, Supersedes: "wf-1"}, "must not supersede"},
		{"empty id", Record{Kind: "workflow", Name: "other", Version: v1}, "id must not be empty"},
		{"empty kind", Record{ID: "wf-9", Name: "other", Version: v1}, "kind must not be empty"},
		{"empty name", Record{ID: "wf-9", Kind: "workflow", Version: v1}, "name must not be empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := l.Register(Registration{Record: c.rec}); err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("want error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestActedOnMarks(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")

	if _, err := l.MarkActedOn("wf-99", ActedOnMark{Actor: ActorCaptain}); err == nil {
		t.Fatal("acted-on on missing record: want error")
	}
	if _, err := l.MarkActedOn("wf-1", ActedOnMark{}); err == nil {
		t.Fatal("acted-on with empty actor: want error")
	}
	if _, err := l.MarkActedOn("wf-1", ActedOnMark{Actor: ActorCaptain, Note: "merged PR #7", At: "2026-08-30T01:00:00Z"}); err != nil {
		t.Fatalf("MarkActedOn: %v", err)
	}
	// Acting on a record that is later superseded keeps the mark.
	mustSupersede(t, l, "wf-2", "wf-1", "1.0.1", ChangeAnnotation, "typo fix")
	marks := l.ActedOnMarks("wf-1")
	if len(marks) != 1 || marks[0].Actor != ActorCaptain || marks[0].Note != "merged PR #7" {
		t.Fatalf("ActedOnMarks(wf-1) = %v; want the captain mark", marks)
	}
	if len(l.ActedOnMarks("wf-2")) != 0 {
		t.Fatal("ActedOnMarks(wf-2): want none")
	}
}

// The ledger must survive its own log: fold(events) == original state, and
// the events round-trip through JSON (the persistence format is one JSON
// object per event).
func TestReplayAndJSONRoundTrip(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
	mustSupersede(t, l, "wf-2", "wf-1", "1.1.0", ChangeAdditive, "add stage")
	if _, err := l.MarkActedOn("wf-2", ActedOnMark{Actor: ActorCaptain, At: "2026-08-30T02:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	mustSupersede(t, l, "wf-3", "wf-2", "2.0.0", ChangeBreaking, "remove stage")

	// JSON round-trip every event.
	events := l.Events()
	var restored []Event
	for _, ev := range events {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal event %d: %v", ev.Seq, err)
		}
		var back Event
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("unmarshal event %d: %v", ev.Seq, err)
		}
		restored = append(restored, back)
	}
	if !reflect.DeepEqual(events, restored) {
		t.Fatalf("JSON round-trip changed events:\n%v\n%v", events, restored)
	}

	// Replay the round-tripped log and compare observable state.
	l2, err := Replay(restored)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !reflect.DeepEqual(l.Events(), l2.Events()) {
		t.Fatal("replayed ledger has a different event log")
	}
	h1, _ := l.Head("triage")
	h2, _ := l2.Head("triage")
	if h1 != h2 {
		t.Fatalf("replayed head %v != original %v", h2, h1)
	}
	if !reflect.DeepEqual(l.History("triage"), l2.History("triage")) {
		t.Fatal("replayed history differs")
	}
	if !reflect.DeepEqual(l.ActedOnMarks("wf-2"), l2.ActedOnMarks("wf-2")) {
		t.Fatal("replayed acted-on marks differ")
	}

	// A corrupted log fails loudly.
	bad := append([]Event{}, restored...)
	bad[1].Record.Supersedes = "wf-99"
	if _, err := Replay(bad); err == nil {
		t.Fatal("Replay of corrupted log: want error")
	}
	gap := append([]Event{}, restored...)
	gap[2].Seq = 99
	if _, err := Replay(gap); err == nil {
		t.Fatal("Replay with seq gap: want error")
	}
}
