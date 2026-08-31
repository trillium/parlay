// Normalization and lead-signal extraction — the deterministic text layer
// everything else keys off. Kept boring on purpose: no stemming, no fuzz,
// no similarity. "Similar input" (#128 §35) is scoped to *same lead signal*
// in v1 (docs/routing.md, gap-fill 2); anything fuzzier is inference's job.
package routing

import (
	"strings"
	"unicode"
)

// NormalizeKey lowercases, replaces every non-letter/non-digit rune with a
// space, and collapses runs of whitespace to single spaces. The result is
// the canonical form rules are stored in and inputs are matched against.
func NormalizeKey(s string) string {
	mapped := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(mapped), " ")
}

// leadSignalMaxWords bounds how long a comma/colon-delimited lead segment
// may be before it stops looking like an address ("Dave," / "hey dave,")
// and starts looking like a clause ("after we talked about it yesterday,").
const leadSignalMaxWords = 3

// LeadSignal extracts the routing key from an input: #128 §34's "first
// portion as a potential routing key". If the raw input has a comma, colon,
// or semicolon whose left segment normalizes to 1..3 words, that segment is
// the signal ("Parlay, auth is broken" → "parlay"; "hey dave: look" →
// "hey dave"). Otherwise the first normalized token is ("dave check the
// thing" → "dave"). An input that normalizes to nothing yields "" — the
// empty signal never matches and never accrues evidence, so unkeyed
// ambiguous messages stay on the inference path every time.
func LeadSignal(raw string) string {
	if i := strings.IndexAny(raw, ",:;"); i >= 0 {
		seg := NormalizeKey(raw[:i])
		if seg != "" && len(strings.Fields(seg)) <= leadSignalMaxWords {
			return seg
		}
	}
	norm := NormalizeKey(raw)
	if norm == "" {
		return ""
	}
	return strings.Fields(norm)[0]
}

// hasWordBoundaryPrefix reports whether key is a word-boundary prefix of
// norm (both already normalized): norm == key, or norm starts with key
// followed by a space. "parlay auth" prefixes "parlay auth is broken" but
// not "parlay authentication is broken".
func hasWordBoundaryPrefix(norm, key string) bool {
	if key == "" {
		return false
	}
	if norm == key {
		return true
	}
	return strings.HasPrefix(norm, key+" ")
}
