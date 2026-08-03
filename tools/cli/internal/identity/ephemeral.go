// Ephemeral hash identity helpers — pure, deterministic, testable.
//
// Ported from packages/cli/src/identity-ephemeral.ts. An ephemeral agent has
// no human-chosen slug: it gets a random hash id ("eph-" + 8 hex chars), a
// derived display name, and a deterministic color computed from the id so
// the same id always paints the same tab accent.
package identity

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// EphemeralHash returns "eph-" + 8 lowercase hex chars from 4 random bytes.
func EphemeralHash() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("eph-%02x%02x%02x%02x", b[0], b[1], b[2], b[3])
}

// GenerateEphemeralID generates an id that does not collide with an existing
// one per the exists predicate. Retries once on collision (spec: "retry once
// if collision"); a second collision — astronomically unlikely across a
// 4-byte space — is left to the caller's on-disk write guard as backstop.
func GenerateEphemeralID(exists func(id string) bool) string {
	first := EphemeralHash()
	if !exists(first) {
		return first
	}
	return EphemeralHash()
}

// EphemeralName derives a display name from an ephemeral id:
// "eph-a3f21b4c" -> "Agent A3F21B4C".
func EphemeralName(id string) string {
	suffix := ""
	if len(id) > 4 {
		suffix = id[4:]
	}
	return "Agent " + strings.ToUpper(suffix)
}

// ColorFromID derives a deterministic, readable hex color from id: an FNV-1a
// 32-bit hash, three bytes of which are each squeezed into the 40-220 range
// so no channel is too dark or too washed out. Same id => same color, always.
// Must match identity-ephemeral.ts's Math.imul(h, 0x01000193) >>> 0 exactly
// (docs/scope-go-cli.md §5 item 8) — hence uint32 arithmetic, not int.
func ColorFromID(id string) string {
	var h uint32 = 0x811c9dc5
	for _, r := range []byte(id) {
		h ^= uint32(r)
		h *= 0x01000193
	}
	const span = 220 - 40 // 180
	chan_ := func(b uint32) string {
		return fmt.Sprintf("%02x", 40+(b%(span+1)))
	}
	r := chan_(h & 0xff)
	g := chan_((h >> 8) & 0xff)
	bch := chan_((h >> 16) & 0xff)
	return "#" + r + g + bch
}

// EphemeralIdentity returns the full {id, name, color} triple for id.
type EphemeralTriple struct {
	ID    string
	Name  string
	Color string
}

func EphemeralIdentity(id string) EphemeralTriple {
	return EphemeralTriple{ID: id, Name: EphemeralName(id), Color: ColorFromID(id)}
}
