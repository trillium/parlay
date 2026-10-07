package timeline

import (
	"strings"
	"testing"
	"time"
)

// TestClaimAtIsInclusiveAtBothEnds: the claim interval is derived from two
// audit lines, and a message stamped exactly at the register or at the
// unregister is inside the relay's window, not outside it. Getting this
// backwards would accuse the relay of losing a message it was, in fact,
// claiming at the moment.
func TestClaimAtIsInclusiveAtBothEnds(t *testing.T) {
	from := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	ev := EnrollmentEvidence{Read: true, Claims: map[string][]Claim{
		"crew-1": {{From: from, HasFrom: true, To: to, HasTo: true}},
	}}
	for _, at := range []time.Time{from, from.Add(time.Nanosecond), to} {
		if claimed, why := ev.ClaimAt("crew-1", at); !claimed {
			t.Errorf("ClaimAt(%s) = false (%s), want claimed", at.Format(time.RFC3339), why)
		}
	}
	for _, at := range []time.Time{from.Add(-time.Nanosecond), to.Add(time.Nanosecond)} {
		if claimed, why := ev.ClaimAt("crew-1", at); claimed || why == "" {
			t.Errorf("ClaimAt(%s) = %v, %q; want not-claimed WITH a reason", at.Format(time.RFC3339), claimed, why)
		}
	}
}

// TestClaimAtUsesTheLatestRelease: a channel claimed, released and claimed
// again has two intervals, and the release of the FIRST one must not be read as
// the end of the second.
func TestClaimAtUsesTheLatestRelease(t *testing.T) {
	from := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	ev := EnrollmentEvidence{Read: true, Claims: map[string][]Claim{
		"crew-1": {
			{From: from, HasFrom: true, To: from.Add(time.Hour), HasTo: true},
			{From: from.Add(2 * time.Hour), HasFrom: true},
		},
	}}
	if claimed, why := ev.ClaimAt("crew-1", from.Add(3*time.Hour)); !claimed {
		t.Errorf("the re-claim is open, so a message after it is covered: %s", why)
	}
	if _, why := ev.ClaimAt("crew-1", from.Add(90*time.Minute)); why == "" ||
		!strings.Contains(why, "last claim on this channel ended at") {
		t.Errorf("the gap between claims is not the relay's window: %q", why)
	}
}

// TestClaimAtNeverAnswersForAnotherChannel: the claim trail is per agent id,
// and one channel's enrollment must never license a verdict about another's.
func TestClaimAtNeverAnswersForAnotherChannel(t *testing.T) {
	from := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	ev := EnrollmentEvidence{Read: true, Claims: map[string][]Claim{
		"crew-1": {{From: from, HasFrom: true}},
	}}
	if claimed, why := ev.ClaimAt("crew-2", from); claimed || !strings.Contains(why, "NO claim for this channel") {
		t.Errorf("crew-1's claim must not cover crew-2: %v %q", claimed, why)
	}
}

// TestClaimAtRefusesToGuess: every shape of "this reader cannot tell" must come
// back not-claimed WITH its reason, never silently claimed and never silently
// empty-handed — the verdict it guards is an accusation.
func TestClaimAtRefusesToGuess(t *testing.T) {
	at := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		ev   EnrollmentEvidence
		want string
	}{
		{"never read", EnrollmentEvidence{}, "could not be read"},
		{"read but unreadable reason", EnrollmentEvidence{Reason: "could not read /tmp/x"}, "could not be read"},
		{"truncated", EnrollmentEvidence{Read: true, Truncated: true}, "exceeded this reader's cap"},
		{"undated claim", EnrollmentEvidence{Read: true, Claims: map[string][]Claim{
			"crew-1": {{Undated: true}}}}, "whose time cannot be read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claimed, why := tc.ev.ClaimAt("crew-1", at)
			if claimed {
				t.Fatalf("claimed = true, want false: %q", why)
			}
			if !strings.Contains(why, tc.want) {
				t.Errorf("reason %q does not contain %q", why, tc.want)
			}
		})
	}
}
