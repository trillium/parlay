// The registry: the full enrolled set, loaded from checked-in declaration
// bytes. Enrollment is a repository change, not an API call
// (docs/source-contracts.md "Enrollment mechanics"), so the enrolled set is a
// pure function of the repo tree: two builds of the same commit agree about
// every contract.
package sourcecontract

import (
	"fmt"
	"sort"
	"strings"
)

// Registry is the validated enrolled set. Construct only via Load.
type Registry struct {
	byName map[string]Contract
}

// Load builds the registry from declaration bytes keyed by file basename
// ("<name>.json"). Per-contract validation plus the cross-contract
// invariants: the declared name must match its filename (so a rename cannot
// silently fork an identity), names are unique by construction of the key
// space, and no event name is claimed by two producers — one name per real
// producer is registry-enforced, not comment-enforced.
//
// Files are processed in sorted key order so the first error reported is
// deterministic.
func Load(raws map[string][]byte) (Registry, error) {
	keys := make([]string, 0, len(raws))
	for k := range raws {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	byName := make(map[string]Contract, len(raws))
	emitOwner := map[string]string{}
	for _, key := range keys {
		c, err := Parse(raws[key])
		if err != nil {
			return Registry{}, fmt.Errorf("%s: %v", key, err)
		}
		if want := c.Name + ".json"; key != want {
			return Registry{}, fmt.Errorf("%s: declares name %q; the file must be named %q", key, c.Name, want)
		}
		for _, name := range c.Emits {
			if owner, taken := emitOwner[name]; taken {
				return Registry{}, fmt.Errorf("%s: emits %q is already claimed by %q — one name per real producer", key, name, owner)
			}
			emitOwner[name] = c.Name
		}
		byName[c.Name] = c
	}
	return Registry{byName: byName}, nil
}

// Len reports how many surfaces are enrolled.
func (r Registry) Len() int { return len(r.byName) }

// ByName returns one enrolled contract.
func (r Registry) ByName(name string) (Contract, bool) {
	c, ok := r.byName[name]
	return c, ok
}

// Names returns every enrolled surface name, sorted.
func (r Registry) Names() []string {
	names := make([]string, 0, len(r.byName))
	for n := range r.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// IngressEventNames is the derived events-ingress allowlist: the union of
// every enrolled observability producer's emits, sorted. An empty registry
// derives an empty allowlist — fail closed, never pass-through.
func (r Registry) IngressEventNames() []string {
	var names []string
	for _, c := range r.byName {
		if c.Trust == TrustObservability {
			names = append(names, c.Emits...)
		}
	}
	return sortedCopy(names)
}

// Capable answers #128 §105's question — which enrolled surfaces can
// represent this interaction — sorted by name.
func (r Registry) Capable(i Interaction) []string {
	var names []string
	for n, c := range r.byName {
		for _, cap := range c.Capabilities {
			if cap == i {
				names = append(names, n)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}

// String summarizes the enrolled set for diagnostics: names with postures,
// never declaration content (the live-command-registry rule — registries
// hold no free-form text — is inherited here).
func (r Registry) String() string {
	parts := make([]string, 0, len(r.byName))
	for _, n := range r.Names() {
		parts = append(parts, fmt.Sprintf("%s(%s)", n, r.byName[n].Trust))
	}
	return "sourcecontract.Registry{" + strings.Join(parts, " ") + "}"
}
