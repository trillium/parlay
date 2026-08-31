// Package sourcecontracts carries the embedded mirror of the repo's
// canonical contracts/sources/ tree — the enrolled source contracts of
// docs/source-contracts.md (#128 §29: a source's metadata contract is
// defined at enrollment, not invented ad hoc) — and the minimal loader the
// enforcement side needs: name, trust posture, emits. Nothing else is read
// here; full schema validation is the tools/cli engine's job
// (tools/cli/internal/sourcecontract), which CI runs against the canonical
// tree.
//
// The mirror exists because this is its own Go module and go:embed cannot
// cross the module boundary to the repo root — the same shape as tools/cli's
// cityscaffold mirror. It is NOT a fork: sync_test.go fails the moment the
// two trees differ, so the canonical contracts/sources/ stays the single
// place enrollment happens. To update:
//
//	rm -rf packages/go-server/internal/sourcecontracts/contracts
//	mkdir packages/go-server/internal/sourcecontracts/contracts
//	cp contracts/sources/*.json packages/go-server/internal/sourcecontracts/contracts/
package sourcecontracts

import (
	"embed"
	"encoding/json"
	"io/fs"
	"sort"
	"strings"
)

// all: so a dot- or underscore-prefixed file landing in the mirror is never
// silently skipped — the sync test must see exactly what the loader sees.
//
//go:embed all:contracts
var contractsFS embed.FS

// Declared is the slice of a source contract that enforcement consumes.
// Unknown fields in the declaration are tolerated here on purpose — schema
// strictness lives in the engine, and a minor (additive) schema change must
// not turn this loader into a second validator that lags it.
type Declared struct {
	Name  string   `json:"name"`
	Trust string   `json:"trust"`
	Emits []string `json:"emits"`
}

// Enrolled returns the declared surfaces, sorted by name. Fail closed, never
// pass-through (docs/source-contracts.md "The security story" lock 3): a
// missing mirror, an unreadable file, or ONE unparseable declaration yields
// an empty set — the consumer derives an empty allowlist and refuses
// everything, rather than half-working from a corrupted registry.
func Enrolled() []Declared {
	entries, err := fs.ReadDir(contractsFS, "contracts")
	if err != nil {
		return nil
	}
	var out []Declared
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := contractsFS.ReadFile("contracts/" + e.Name())
		if err != nil {
			return nil
		}
		var d Declared
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
