package store

import "testing"

// Only OFF entries are retained: turning something back on removes it, so the
// list is exactly "what is off right now" and never a log of every flip.
func TestOffSwitchKeepsOnlyWhatIsOff(t *testing.T) {
	s := NewOffSwitch()
	if !s.IsEmpty() {
		t.Fatal("a new switch is not empty")
	}
	if _, _, err := s.Set(OffKindConnection, "dev-1", true, "tester", "website"); err != nil {
		t.Fatal(err)
	}
	if !s.IsConnectionOff("dev-1") {
		t.Fatal("connection was not turned off")
	}
	if got := s.List(); len(got) != 1 || got[0].Kind != OffKindConnection || got[0].ID != "dev-1" {
		t.Fatalf("list = %+v, want one connection entry for dev-1", got)
	}
	if got := s.List(); got[0].Surface != "website" {
		t.Errorf("surface = %q, want website", got[0].Surface)
	}
	if got := s.List(); got[0].At == "" {
		t.Error("entry has no timestamp")
	}

	_, existed, err := s.Set(OffKindConnection, "dev-1", false, "tester", "cli")
	if err != nil {
		t.Fatal(err)
	}
	if !existed {
		t.Error("clearing an entry that existed reported it did not")
	}
	if s.IsConnectionOff("dev-1") {
		t.Fatal("connection is still off after being turned back on")
	}
	if !s.IsEmpty() {
		t.Fatal("turning the last target on left entries behind")
	}
}

// The two target kinds are independent, and the lookup for one never answers
// for the other — a device id and a command id are different namespaces.
func TestOffSwitchKindsAreIndependent(t *testing.T) {
	s := NewOffSwitch()
	if _, _, err := s.Set(OffKindAction, "clear", true, "tester", "api"); err != nil {
		t.Fatal(err)
	}
	if !s.IsActionOff("clear") {
		t.Fatal("action was not turned off")
	}
	if s.IsConnectionOff("clear") {
		t.Error("an action mute answered a connection lookup for the same id")
	}
	if s.Count(OffKindConnection) != 0 || s.Count(OffKindAction) != 1 {
		t.Errorf("counts = connection:%d action:%d, want 0/1",
			s.Count(OffKindConnection), s.Count(OffKindAction))
	}
}

// An unknown kind or a missing id is refused without mutating anything.
func TestOffSwitchRefusesBadInput(t *testing.T) {
	s := NewOffSwitch()
	if _, _, err := s.Set("nonsense", "x", true, "", ""); err == nil {
		t.Error("unknown kind was accepted")
	}
	if _, _, err := s.Set(OffKindConnection, "", true, "", ""); err == nil {
		t.Error("empty id was accepted")
	}
	if _, _, err := s.Set(OffKindAction, "  ", true, "", ""); err == nil {
		t.Error("whitespace-only id was accepted")
	}
	if !s.IsEmpty() {
		t.Fatal("a refused Set still wrote to the switch")
	}
	if err := ValidateOffKind(OffKindConnection); err != nil {
		t.Errorf("connection is not a valid kind: %v", err)
	}
	if err := ValidateOffKind(OffKindAction); err != nil {
		t.Errorf("action is not a valid kind: %v", err)
	}
}

// Setting a target off twice is idempotent and updates attribution rather than
// erroring — an operator re-issuing a mute is not a mistake worth failing.
func TestOffSwitchRepeatedSetIsIdempotent(t *testing.T) {
	s := NewOffSwitch()
	if _, created, err := s.Set(OffKindConnection, "dev-1", true, "first", "cli"); err != nil || !created {
		t.Fatalf("first set: created=%v err=%v", created, err)
	}
	entry, created, err := s.Set(OffKindConnection, "dev-1", true, "second", "website")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("re-setting an already-off target reported a new entry")
	}
	if entry.By != "second" || entry.Surface != "website" {
		t.Errorf("attribution = %q/%q, want second/website", entry.By, entry.Surface)
	}
	if len(s.List()) != 1 {
		t.Errorf("re-setting created a duplicate: %+v", s.List())
	}
}

// A surface that is not one of the three named ones is stored as "" rather than
// as free text — attribution is explanatory and must never carry prose into a
// rendered field.
func TestOffSwitchSurfaceIsWhitelisted(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"website", "website"},
		{" CLI ", "cli"},
		{"api", "api"},
		{"something-else", ""},
		{"<script>", ""},
	} {
		if got := NormalizeOffSurface(tc.in); got != tc.want {
			t.Errorf("NormalizeOffSurface(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A nil switch is legal and empty, so a handler built without one cannot panic.
func TestNilOffSwitchIsEmpty(t *testing.T) {
	var s *OffSwitch
	if !s.IsEmpty() {
		t.Error("a nil switch is not empty")
	}
	if s.IsConnectionOff("d") || s.IsActionOff("a") {
		t.Error("a nil switch reported something off")
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("a nil switch listed %d entries", len(got))
	}
}
