package store

import (
	"strings"
	"time"
)

// CompareStamps orders two RFC3339Nano timestamps by the instant they name,
// returning -1, 0 or 1. Comparing the strings themselves is wrong:
// RFC3339Nano drops trailing zeros, so "…:05Z" sorts AFTER "…:05.1Z" ('Z' is
// greater than '.') although it is earlier, and equal-length fractions of
// different width tie-break on the wrong digit. A stamp that does not parse
// falls back to the lexical order, so the sort stays total.
func CompareStamps(a, b string) int {
	ta, errA := time.Parse(time.RFC3339Nano, a)
	tb, errB := time.Parse(time.RFC3339Nano, b)
	if errA != nil || errB != nil {
		return strings.Compare(a, b)
	}
	switch {
	case ta.Before(tb):
		return -1
	case ta.After(tb):
		return 1
	}
	return 0
}
