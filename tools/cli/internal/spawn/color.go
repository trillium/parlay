package spawn

import "fmt"

// colorFromId computes a deterministic, readable hex color from an agent id.
//
// Must stay bit-identical to the two other independent implementations of
// this same algorithm: packages/cli/src/identity-ephemeral.ts (JS, FNV-1a
// with Math.imul + >>> 0 wraparound) and bin/parlay-spawn's color_from_id()
// (bash, FNV-1a with & 0xffffffff masking). A drift in any one silently
// changes tab colors for batch-spawned/ephemeral agents without erroring —
// see docs/scope-go-spawn.md §5. Agent ids are ASCII kebab-slugs, so
// iterating Go string bytes matches JS charCodeAt() and bash's per-byte loop.
func colorFromId(id string) string {
	var h uint32 = 0x811c9dc5
	for i := 0; i < len(id); i++ {
		h ^= uint32(id[i])
		h *= 16777619 // uint32 multiplication wraps, matching >>> 0 / & 0xffffffff
	}
	chan_ := func(b uint32) uint32 { return 40 + (b % 181) }
	r := chan_(h & 0xff)
	g := chan_((h >> 8) & 0xff)
	b := chan_((h >> 16) & 0xff)
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}
