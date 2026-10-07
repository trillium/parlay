package liveness

import (
	"strings"
	"testing"
	"time"
)

// base is a fixed clock so every age in these tests is exact.
var base = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func stamp(d time.Duration) string { return base.Add(-d).UTC().Format(time.RFC3339) }

// obs returns an observation for a registered agent with a live listener and a
// readable server snapshot, which each test then degrades in ONE way — so a
// failure names the single distinction that broke.
func obs() Observation {
	return Observation{
		RegistryKnown:  true,
		Registered:     true,
		ListenersKnown: true,
		HasListener:    true,
		PresenceKnown:  true,
		Now:            base,
		Window:         10 * time.Minute,
	}
}

func TestRegistryUnreadIsUnknownNotOffline(t *testing.T) {
	o := obs()
	o.RegistryKnown = false
	o.Registered = false
	o.PresenceKnown = false
	o.HasPresenceRow = false
	v := Classify(o)
	if v.State != StateUnknown {
		t.Fatalf("state = %q, want %q — an unreadable server is not an empty fleet", v.State, StateUnknown)
	}
	if !strings.Contains(v.StateNote, "unknown") {
		t.Errorf("state note should name the unknown: %q", v.StateNote)
	}
	if v.Heartbeat != HeartbeatUnknown {
		t.Errorf("heartbeat = %q, want %q — the presence snapshot came from the same failed read", v.Heartbeat, HeartbeatUnknown)
	}
}

func TestOfflineIsOnlyWhenTheServerAnswered(t *testing.T) {
	o := obs()
	o.Registered = false
	o.HasListener = false
	o.HasPresenceRow = false
	if v := Classify(o); v.State != StateOffline {
		t.Fatalf("state = %q, want %q", v.State, StateOffline)
	}
}

func TestRegisteredWithoutListenerIsGhost(t *testing.T) {
	o := obs()
	o.HasListener = false
	v := Classify(o)
	if v.State != StateGhost {
		t.Fatalf("state = %q, want %q", v.State, StateGhost)
	}
	if !strings.Contains(v.StateNote, "nothing listening") {
		t.Errorf("ghost note should say nothing is listening: %q", v.StateNote)
	}
}

// A failed process-table probe is not evidence of a dead listener: reporting a
// working agent as a ghost sends an operator to clear its registration.
func TestFailedProbeNeverBecomesGhost(t *testing.T) {
	o := obs()
	o.ListenersKnown = false
	o.HasListener = false
	v := Classify(o)
	if v.State != StateLive {
		t.Fatalf("state = %q, want %q on an unreadable process table", v.State, StateLive)
	}
	if !strings.Contains(v.StateNote, "cannot be confirmed") {
		t.Errorf("state note must admit the probe failed: %q", v.StateNote)
	}
}

// The four heartbeat shapes are four different facts. Only the first two are
// ages; the last two are absence, and calling absence "stale" is the lie this
// package exists to prevent.
func TestHeartbeatShapesStayDistinct(t *testing.T) {
	cases := []struct {
		name      string
		presence  bool
		hasRow    bool
		lastSeen  string
		want      string
		noteHas   string
		wantStamp bool
	}{
		{"stamp inside the window is fresh", true, true, stamp(2 * time.Minute), HeartbeatFresh, "", true},
		{"stamp past the window is expired", true, true, stamp(2 * time.Hour), HeartbeatExpired, "past the", true},
		{"row with no stamp is never observed", true, true, "", HeartbeatNeverObserved, "absent, not expired", false},
		{"no row at all", true, false, "", HeartbeatNoRow, "no presence row", false},
		{"snapshot unreadable", false, false, "", HeartbeatUnknown, "did not answer", false},
		{"unparseable stamp", true, true, "not-a-time", HeartbeatUnknown, "does not parse", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := obs()
			o.PresenceKnown = tc.presence
			o.HasPresenceRow = tc.hasRow
			o.LastSeen = tc.lastSeen
			v := Classify(o)
			if v.Heartbeat != tc.want {
				t.Fatalf("heartbeat = %q, want %q", v.Heartbeat, tc.want)
			}
			if tc.noteHas != "" && !strings.Contains(v.HeartbeatNote, tc.noteHas) {
				t.Errorf("heartbeat note %q should contain %q", v.HeartbeatNote, tc.noteHas)
			}
			// A parsed stamp exists exactly for the two AGES; the two shapes of
			// absence must carry no stamp at all, or an operator could compute an
			// age from an absent heartbeat.
			if v.HeartbeatAt.IsZero() == tc.wantStamp {
				t.Errorf("HeartbeatAt zero = %v, wantStamp = %v", v.HeartbeatAt.IsZero(), tc.wantStamp)
			}
		})
	}
}

// HeartbeatFor is the channel record's own age, and must not be the silence
// duration — the two can come from different records.
func TestHeartbeatAgeIsItsOwnNotTheSilence(t *testing.T) {
	o := obs()
	o.HasPresenceRow = true
	o.LastSeen = stamp(30 * time.Minute)
	o.Activities = []Activity{{Source: SourceStatus, Detail: `status "working"`, At: base.Add(-5 * time.Second)}}
	v := Classify(o)
	if v.HeartbeatFor != 30*time.Minute {
		t.Errorf("HeartbeatFor = %s, want 30m (the channel stamp's age)", v.HeartbeatFor)
	}
	if v.SilentFor != 5*time.Second {
		t.Errorf("SilentFor = %s, want 5s (the newest record of any source)", v.SilentFor)
	}
}

// The false-alarm case this whole package is shaped around: a working agent
// that has not spoken. Its channel stamp is stale; its status file is fresh.
func TestFreshLocalActivityBeatsAStaleChannelStamp(t *testing.T) {
	o := obs()
	o.HasPresenceRow = true
	o.LastSeen = stamp(3 * time.Hour)
	o.Activities = []Activity{{Source: SourceStatus, Detail: `status "working"`, At: base.Add(-20 * time.Second)}}
	v := Classify(o)
	if v.Heartbeat != HeartbeatExpired {
		t.Errorf("heartbeat = %q, want %q — the channel really is stale", v.Heartbeat, HeartbeatExpired)
	}
	if v.Silence != SilenceFresh {
		t.Fatalf("silence = %q, want %q — the status file is 20s old, so the agent is NOT silent", v.Silence, SilenceFresh)
	}
	if v.LastSource != SourceStatus {
		t.Errorf("last source = %q, want %q", v.LastSource, SourceStatus)
	}
}

func TestSilenceExpiresOnTheNewestRecord(t *testing.T) {
	o := obs()
	o.HasPresenceRow = true
	o.LastSeen = stamp(9 * time.Hour)
	o.Activities = []Activity{
		{Source: SourceChannel, Detail: "channel activity", At: base.Add(-9 * time.Hour)},
		{Source: SourceDelivery, Detail: "relay spooled", At: base.Add(-2 * time.Hour)},
	}
	v := Classify(o)
	if v.Silence != SilenceExpired {
		t.Fatalf("silence = %q, want %q", v.Silence, SilenceExpired)
	}
	if v.SilentFor != 2*time.Hour {
		t.Errorf("SilentFor = %s, want 2h (the newest record, not the oldest)", v.SilentFor)
	}
	if v.LastSource != SourceDelivery || v.LastDetail != "relay spooled" {
		t.Errorf("last = %s/%s, want delivery/relay spooled", v.LastSource, v.LastDetail)
	}
	if v.SilentSince.IsZero() {
		t.Error("SilentSince must be set whenever silence is expired")
	}
}

// No dated record is unmeasurable, NOT zero: an empty record is not evidence
// that nothing happened.
func TestNoDatedRecordIsUnknownAndSaysWhatWasLookedAt(t *testing.T) {
	o := obs()
	o.HasPresenceRow = false
	o.Looked = []string{"the server's presence row for this channel", "the status file /x/crew-9/status"}
	v := Classify(o)
	if v.Silence != SilenceUnknown {
		t.Fatalf("silence = %q, want %q", v.Silence, SilenceUnknown)
	}
	if v.SilentFor != 0 {
		t.Errorf("SilentFor = %s, want 0 — unknown silence has no duration", v.SilentFor)
	}
	if v.LastKnown {
		t.Error("LastKnown must be false with no dated record")
	}
	for _, want := range []string{"no dated activity record", "the status file /x/crew-9/status", "not zero"} {
		if !strings.Contains(v.SilenceNote, want) {
			t.Errorf("silence note %q should contain %q", v.SilenceNote, want)
		}
	}
}

func TestNothingReadAtAllSaysSo(t *testing.T) {
	v := Classify(Observation{Now: base})
	if !strings.Contains(v.SilenceNote, "no activity record could be read at all") {
		t.Errorf("silence note = %q", v.SilenceNote)
	}
}

func TestDefaultWindowAndClock(t *testing.T) {
	o := obs()
	o.Now = time.Time{}
	o.Window = 0
	o.HasPresenceRow = true
	o.LastSeen = time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339)
	v := Classify(o)
	if v.Heartbeat != HeartbeatFresh {
		t.Fatalf("heartbeat = %q, want %q with the default window", v.Heartbeat, HeartbeatFresh)
	}
}

func TestFutureStampIsNotNegativeSilence(t *testing.T) {
	o := obs()
	o.HasPresenceRow = true
	o.LastSeen = base.Add(5 * time.Minute).UTC().Format(time.RFC3339)
	v := Classify(o)
	if v.Heartbeat != HeartbeatFresh || v.HeartbeatFor != 0 {
		t.Errorf("future stamp: heartbeat=%q for=%s — want fresh/0s", v.Heartbeat, v.HeartbeatFor)
	}
	// The channel stamp is folded in as an activity candidate, so the agent is
	// not reported as having no record at all.
	if v.Silence != SilenceFresh || v.SilentFor != 0 || v.LastSource != SourceChannel {
		t.Errorf("future stamp: silence=%q for=%s source=%q — want fresh/0s/channel", v.Silence, v.SilentFor, v.LastSource)
	}
}

// The channel stamp alone is an activity candidate: a row with a stamp must
// produce a dated last-activity even when the caller passes no other record.
func TestChannelStampAloneIsADatedRecord(t *testing.T) {
	o := obs()
	o.HasPresenceRow = true
	o.LastSeen = stamp(3 * time.Hour)
	v := Classify(o)
	if !v.LastKnown || v.LastSource != SourceChannel || v.SilentFor != 3*time.Hour {
		t.Errorf("last = %v/%q/%s, want true/channel/3h", v.LastKnown, v.LastSource, v.SilentFor)
	}
}

func TestRelayAnswerTravelsThrough(t *testing.T) {
	o := obs()
	o.RelayKnown = true
	o.RelayEnrolled = true
	v := Classify(o)
	if !v.RelayKnown || !v.RelayEnrolled {
		t.Errorf("relay answer dropped: known=%v enrolled=%v", v.RelayKnown, v.RelayEnrolled)
	}
}

func TestShort(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m"},
		{134 * time.Minute, "2h14m"},
		{72 * time.Hour, "3d"},
	}
	for _, tc := range cases {
		if got := Short(tc.in); got != tc.want {
			t.Errorf("Short(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseStamp(t *testing.T) {
	if _, ok := ParseStamp("2026-10-07T12:00:00Z"); !ok {
		t.Error("RFC3339 stamp should parse")
	}
	if _, ok := ParseStamp("  2026-10-07T12:00:00.123456789Z "); !ok {
		t.Error("RFC3339Nano stamp should parse, whitespace trimmed")
	}
	if _, ok := ParseStamp(""); ok {
		t.Error("empty stamp must not parse — it is the server's 'never observed' shape")
	}
	if _, ok := ParseStamp("null"); ok {
		t.Error("a literal null must not parse")
	}
}

// --- the on-disk roster (the server did not answer) -------------------------

// TestDiskRosterListedIsLiveWithTheSubstitutionNamed: the verdict is the same
// one a live registry would give, but the WORDS must say where the roster came
// from — a registration answered off a file is not a live answer, and a surface
// that printed it as one would hide that the server is down.
func TestDiskRosterListedIsLiveWithTheSubstitutionNamed(t *testing.T) {
	o := obs()
	o.RegistryFromDisk = true
	o.PresenceKnown = false
	o.HasPresenceRow = false
	o.LastSeen = ""
	v := Classify(o)
	if v.State != StateLive {
		t.Fatalf("state = %q, want %q", v.State, StateLive)
	}
	if !strings.Contains(v.StateNote, "on-disk registry") || !strings.Contains(v.StateNote, "last persisted") {
		t.Errorf("state note must name the disk substitution: %q", v.StateNote)
	}
	// Presence is never written to disk, so the heartbeat cannot be answered
	// from the same file — and it must say WHY rather than just "unknown".
	if v.Heartbeat != HeartbeatUnknown {
		t.Fatalf("heartbeat = %q, want %q", v.Heartbeat, HeartbeatUnknown)
	}
	if !strings.Contains(v.HeartbeatNote, "never written to disk") {
		t.Errorf("heartbeat note must say presence is never on disk: %q", v.HeartbeatNote)
	}
}

// TestDiskRosterListedWithoutAListenerIsGhost: the process table is a LOCAL
// measurement and stays trustworthy when the server is dead, so a listed agent
// with nothing listening is a ghost the operator can act on.
func TestDiskRosterListedWithoutAListenerIsGhost(t *testing.T) {
	o := obs()
	o.RegistryFromDisk = true
	o.HasListener = false
	o.PresenceKnown = false
	v := Classify(o)
	if v.State != StateGhost {
		t.Fatalf("state = %q, want %q", v.State, StateGhost)
	}
	if !strings.Contains(v.StateNote, "on-disk registry") {
		t.Errorf("state note must name the disk substitution: %q", v.StateNote)
	}
}

// TestDiskRosterSilentOnThisAgentIsUnknownNotOffline is the honesty rule that
// makes the fallback safe: "not in the file this CLI read" is not "not
// enrolled", because which state directory the server runs with is a separate
// configuration point. A wrong offline sends an operator to re-register a
// healthy agent; unknown is the only supportable answer.
func TestDiskRosterSilentOnThisAgentIsUnknownNotOffline(t *testing.T) {
	o := obs()
	o.RegistryFromDisk = true
	o.Registered = false
	o.HasListener = false
	o.PresenceKnown = false
	v := Classify(o)
	if v.State != StateUnknown {
		t.Fatalf("state = %q, want %q — a roster read off disk cannot settle enrollment", v.State, StateUnknown)
	}
	if !strings.Contains(v.StateNote, "unknown, not settled") {
		t.Errorf("state note must say what is unsettled: %q", v.StateNote)
	}
}

// TestDiskRosterDoesNotQuietTheOrdinaryPath: when the server answered, nothing
// about the disk fallback may appear — the substitution is only ever named
// when it was actually made.
func TestDiskRosterDoesNotQuietTheOrdinaryPath(t *testing.T) {
	o := obs()
	o.HasPresenceRow = true
	o.LastSeen = stamp(30 * time.Second)
	v := Classify(o)
	if v.State != StateLive {
		t.Fatalf("state = %q, want %q", v.State, StateLive)
	}
	if v.StateNote != "" {
		t.Errorf("a healthy registered agent needs no state note, got %q", v.StateNote)
	}
	if v.HeartbeatNote != "" {
		t.Errorf("a fresh heartbeat needs no note, got %q", v.HeartbeatNote)
	}
}
