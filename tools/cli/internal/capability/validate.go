// Declaration validation: strict and loud (docs/interface-capabilities.md
// "The schema"). An invalid declaration refuses the connection rather than
// falling back to legacy full delivery — fail-open would widen what a
// narrowing surface receives.
package capability

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/trillium/parlay/tools/cli/internal/supersession"
)

const (
	// SupportedSchemaMajor: the declaration-contract major this engine
	// implements. A different major is refused loudly — the surface must
	// know it was not understood, not be half-understood.
	SupportedSchemaMajor = 1
	// MaxDeclarationBytes bounds the raw declaration payload. Declarations
	// are a handful of names and flags; anything larger is a bug or junk
	// aimed at an unauthenticated server's memory.
	MaxDeclarationBytes = 8 * 1024
	// MaxAccepts / MaxTokens bound the axis entry counts.
	MaxAccepts = 64
	MaxTokens  = 32
)

// nameRE constrains capability/kind/token names: lowercase snake, the shape
// of every existing SSE event name.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// instanceRE constrains the surface instance id (the panel's device uuid).
var instanceRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// Validate rejects a declaration this engine cannot faithfully enforce.
func (d *Declaration) Validate() error {
	v, err := supersession.ParseVersion(d.Schema)
	if err != nil {
		return fmt.Errorf("capability declaration: schema: %v", err)
	}
	if v.Major != SupportedSchemaMajor {
		return fmt.Errorf("capability declaration: schema major %d unsupported (this engine implements %d)",
			v.Major, SupportedSchemaMajor)
	}
	if !nameRE.MatchString(d.Surface.Kind) {
		return fmt.Errorf("capability declaration: surface.kind %q: want %s", d.Surface.Kind, nameRE)
	}
	if d.Surface.Instance != "" && !instanceRE.MatchString(d.Surface.Instance) {
		return fmt.Errorf("capability declaration: surface.instance %q: want %s", d.Surface.Instance, instanceRE)
	}
	if len(d.Accepts) > MaxAccepts {
		return fmt.Errorf("capability declaration: %d accepts entries exceeds the %d cap", len(d.Accepts), MaxAccepts)
	}
	for name := range d.Accepts {
		if !nameRE.MatchString(name) {
			return fmt.Errorf("capability declaration: accepts name %q: want %s", name, nameRE)
		}
	}
	for axis, tokens := range map[string][]string{"content": d.Content, "interactions": d.Interactions} {
		if len(tokens) > MaxTokens {
			return fmt.Errorf("capability declaration: %d %s entries exceeds the %d cap", len(tokens), axis, MaxTokens)
		}
		for _, tok := range tokens {
			if !nameRE.MatchString(tok) {
				return fmt.Errorf("capability declaration: %s token %q: want %s", axis, tok, nameRE)
			}
		}
	}
	return nil
}

// ParseDeclaration parses and validates one raw declaration payload.
// Unknown top-level fields are deliberately ignored (LSP's ignore-unknown
// posture): an additive minor-bump field from a newer surface must not
// break this server.
func ParseDeclaration(raw []byte) (*Declaration, error) {
	if len(raw) > MaxDeclarationBytes {
		return nil, fmt.Errorf("capability declaration: %d bytes exceeds the %d cap", len(raw), MaxDeclarationBytes)
	}
	var d Declaration
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("capability declaration: %v", err)
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}
