package parlaybeads

import "context"

// AffirmativelyClosed reports whether the store AFFIRMATIVELY says the bead
// is closed. It is the fail-open oracle carried over from
// identity/worklink.go's BoundWorkItemClosed contract: a lookup that merely
// FAILED — store unreachable, id missing, any error at all — is NOT evidence
// of a closed bead and returns false, so a store hiccup can never suppress a
// legitimate relaunch or trigger a teardown of healthy work.
//
// Callers gating suppressive/destructive behavior on closed-ness MUST come
// through here rather than calling Get and interpreting errors themselves —
// one oracle, so no two call sites can disagree about what a failed lookup
// means.
func AffirmativelyClosed(ctx context.Context, c Client, id string) bool {
	if c == nil || id == "" {
		return false
	}
	b, err := c.Get(ctx, id)
	if err != nil {
		return false
	}
	return b.Closed()
}
