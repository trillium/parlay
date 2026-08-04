// Ported from packages/cli/src/identity-ephemeral.test.ts — unit tests for
// the pure ephemeral-identity helpers.
package identity

import (
	"regexp"
	"strconv"
	"testing"
)

var ephemeralIDRe = regexp.MustCompile(`^eph-[0-9a-f]{8}$`)
var hexColorRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func TestEphemeralHashYields8LowercaseHexChars(t *testing.T) {
	for i := 0; i < 50; i++ {
		id := EphemeralHash()
		if !ephemeralIDRe.MatchString(id) {
			t.Fatalf("EphemeralHash() = %q, want eph-<8 lowercase hex>", id)
		}
	}
}

func TestGenerateEphemeralIDReturnsFirstCandidateWhenNoCollision(t *testing.T) {
	id := GenerateEphemeralID(func(string) bool { return false })
	if !ephemeralIDRe.MatchString(id) {
		t.Fatalf("GenerateEphemeralID() = %q, want eph-<8 lowercase hex>", id)
	}
}

func TestGenerateEphemeralIDRetriesOnceOnCollision(t *testing.T) {
	calls := 0
	var seen []string
	id := GenerateEphemeralID(func(candidate string) bool {
		calls++
		seen = append(seen, candidate)
		return calls == 1 // only the first candidate collides
	})
	if !ephemeralIDRe.MatchString(id) {
		t.Fatalf("GenerateEphemeralID() = %q, want eph-<8 lowercase hex>", id)
	}
	if calls != 1 {
		t.Fatalf("exists predicate called %d times, want 1 (retry is not re-checked)", calls)
	}
	if id == seen[0] {
		t.Fatalf("GenerateEphemeralID() returned the colliding id %q", id)
	}
}

func TestColorFromIDValidAndDeterministic(t *testing.T) {
	c1 := ColorFromID("eph-a3f21b4c")
	c2 := ColorFromID("eph-a3f21b4c")
	if !hexColorRe.MatchString(c1) {
		t.Fatalf("ColorFromID() = %q, want #rrggbb", c1)
	}
	if c1 != c2 {
		t.Fatalf("ColorFromID() not deterministic: %q != %q", c1, c2)
	}
}

func TestColorFromIDKeepsChannelsInReadableRange(t *testing.T) {
	for _, id := range []string{"eph-00000000", "eph-ffffffff", "eph-deadbeef", "mayor", "fable", "x"} {
		c := ColorFromID(id)
		if !hexColorRe.MatchString(c) {
			t.Fatalf("ColorFromID(%q) = %q, want #rrggbb", id, c)
		}
		for _, part := range [][2]int{{1, 3}, {3, 5}, {5, 7}} {
			v, err := strconv.ParseInt(c[part[0]:part[1]], 16, 32)
			if err != nil {
				t.Fatalf("ColorFromID(%q) channel %q not hex: %v", id, c[part[0]:part[1]], err)
			}
			if v < 0x28 || v > 0xdc {
				t.Fatalf("ColorFromID(%q) channel %d out of [0x28,0xdc] range", id, v)
			}
		}
	}
}

func TestColorFromIDDiffersForDifferentIDs(t *testing.T) {
	a := ColorFromID("eph-11111111")
	b := ColorFromID("eph-22222222")
	if a == b {
		t.Fatalf("ColorFromID produced same color %q for different ids", a)
	}
}

func TestEphemeralNameIsAgentPlusUppercasedSuffix(t *testing.T) {
	got := EphemeralName("eph-a3f21b4c")
	want := "Agent A3F21B4C"
	if got != want {
		t.Fatalf("EphemeralName() = %q, want %q", got, want)
	}
}

func TestEphemeralIdentityBundlesIDNameColorConsistently(t *testing.T) {
	id := "eph-deadbeef"
	ident := EphemeralIdentity(id)
	if ident.ID != id {
		t.Fatalf("EphemeralIdentity().ID = %q, want %q", ident.ID, id)
	}
	if ident.Name != "Agent DEADBEEF" {
		t.Fatalf("EphemeralIdentity().Name = %q, want %q", ident.Name, "Agent DEADBEEF")
	}
	if ident.Color != ColorFromID(id) {
		t.Fatalf("EphemeralIdentity().Color = %q, want %q", ident.Color, ColorFromID(id))
	}
}
