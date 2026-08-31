// Mirror-only file (see sync_test.go): the canonical engine's validate.go
// calls supersession.ParseVersion for the schema field, but that lives in
// tools/cli/internal/supersession — a different module, unreachable across
// Go's internal/ visibility boundary. This shim reproduces ParseVersion's
// exact strictness (three plain non-negative decimal fields; signs,
// prerelease tags, a leading "v", and empty fields all refuse loudly) so a
// declaration accepted by one server is accepted by the other.
// TestVersionShimMatchesSupersessionParseVersion pins this text to the
// canonical function body — if supersession.ParseVersion changes, that test
// fails and this shim must be re-derived, never patched independently.
package capability

import (
	"fmt"
	"strconv"
	"strings"
)

// schemaVersion is the local stand-in for supersession.Version, carrying
// exactly the fields Validate consults.
type schemaVersion struct {
	Major int
	Minor int
	Patch int
}

func parseSchemaVersion(s string) (schemaVersion, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return schemaVersion{}, fmt.Errorf("version %q: want MAJOR.MINOR.PATCH", s)
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || strings.TrimLeft(p, "0123456789") != "" {
			return schemaVersion{}, fmt.Errorf("version %q: field %q is not a plain non-negative integer", s, p)
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return schemaVersion{}, fmt.Errorf("version %q: %v", s, err)
		}
		n[i] = v
	}
	return schemaVersion{Major: n[0], Minor: n[1], Patch: n[2]}, nil
}
