package evalengine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ── The command manifest: DATA that the engine loads and interprets ─────────────
//
// A manifest is the POLICY half of the split: which commands exist, their
// phrases/mode/priority/description/enabled, and — per command — either a
// declarative `sequence` of closed action verbs or a `handler` delegation. The Go
// engine parses, validates against the closed registries, and interprets it. No
// command shape here is compiled in; the embedded default (default_commands.json)
// is a fallback, not the source of truth.

// manifestSchema is the only schema id this engine accepts.
const manifestSchema = "parlay.commands/v1"

// Manifest is the top-level document: a header plus an ordered command list.
type Manifest struct {
	Schema   string            `json:"schema"`
	Version  string            `json:"version"`
	Commands []CommandManifest `json:"commands"`
}

// CommandManifest is one command as data. Enabled is a pointer so an omitted field
// defaults to true (an explicit false disables the command). Platforms is the set of
// surfaces the command is eligible on (empty ⇒ the default platform); every platform
// listed must implement the verbs/handler the command emits (checked at load).
type CommandManifest struct {
	ID          string   `json:"id"`
	Phrases     []string `json:"phrases"`
	Mode        string   `json:"mode"`
	Priority    int      `json:"priority"`
	Description string   `json:"description"`
	Enabled     *bool    `json:"enabled"`
	Platforms   []string `json:"platforms,omitempty"`
	Emit        Emit     `json:"emit"`
}

// isEnabled reports whether the command participates in matching (default true).
func (c CommandManifest) isEnabled() bool { return c.Enabled == nil || *c.Enabled }

// Emit is a command's behavior — exactly one of two kinds:
//   - "sequence": an ordered list of closed action verbs with arg expressions,
//     plus onResolveFail governing what happens when a resolver misses.
//   - "handler":  delegation to a named stateful handler with a static config.
type Emit struct {
	Kind          string          `json:"kind"`
	OnResolveFail string          `json:"onResolveFail,omitempty"`
	Actions       []EmitAction    `json:"actions,omitempty"`
	Handler       string          `json:"handler,omitempty"`
	Config        json.RawMessage `json:"config,omitempty"`
}

// EmitAction is one closed verb plus its named arg expressions.
type EmitAction struct {
	Verb string             `json:"verb"`
	Args map[string]ArgExpr `json:"args,omitempty"`
}

// submitConfig is the typed shape of a `submit` handler's config. Kept minimal —
// today's builtin ships {delayMs:1000, requireTail:true}.
type submitConfig struct {
	DelayMs     int  `json:"delayMs"`
	RequireTail bool `json:"requireTail"`
}

// ── Arg expressions ─────────────────────────────────────────────────────────────

// argKind discriminates the four arg-value shapes.
type argKind int

const (
	argInterp    argKind = iota // a string, possibly containing {capture} tokens
	argNumber                   // an integer literal
	argBool                     // a boolean literal
	argResolve                  // { "resolve": name, "from": expr }
	argTransform                // { "transform": name, "from": expr }
)

// ArgExpr is a single action-arg value: a literal, a capture interpolation, a
// resolve expression, or a transform expression. It parses itself from JSON so the
// manifest stays clean typed data.
type ArgExpr struct {
	Kind argKind
	Str  string // argInterp: the raw string (may contain {name})
	Num  int    // argNumber
	Bool bool   // argBool
	Name string // argResolve/argTransform: registry key
	From string // argResolve/argTransform: source expr (literal or {capture})
}

// UnmarshalJSON discriminates by the first JSON token: a string is an
// interpolation, an object is a resolve/transform expression, and a bare
// number/bool is a literal.
func (a *ArgExpr) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return fmt.Errorf("empty arg expression")
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		a.Kind = argInterp
		a.Str = s
		return nil
	case '{':
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return err
		}
		if raw, ok := obj["resolve"]; ok {
			a.Kind = argResolve
			if err := json.Unmarshal(raw, &a.Name); err != nil {
				return err
			}
		} else if raw, ok := obj["transform"]; ok {
			a.Kind = argTransform
			if err := json.Unmarshal(raw, &a.Name); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("arg object must have a \"resolve\" or \"transform\" key")
		}
		if raw, ok := obj["from"]; ok {
			if err := json.Unmarshal(raw, &a.From); err != nil {
				return err
			}
		}
		return nil
	case 't', 'f':
		a.Kind = argBool
		return json.Unmarshal(trimmed, &a.Bool)
	default: // number
		a.Kind = argNumber
		return json.Unmarshal(trimmed, &a.Num)
	}
}

// ── Parse + validate ────────────────────────────────────────────────────────────

// parseManifest decodes and fully validates a manifest against the closed
// registries. It returns an error (and no manifest) on ANY problem — the caller
// keeps its prior good set (fail-closed). A valid manifest guarantees every
// command compiles and references only known verbs/resolvers/transforms/handlers,
// so the interpreter can run without runtime "unknown X" surprises.
func parseManifest(data []byte) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var man Manifest
	if err := dec.Decode(&man); err != nil {
		return nil, fmt.Errorf("manifest decode: %w", err)
	}
	if err := validateManifest(&man); err != nil {
		return nil, err
	}
	return &man, nil
}

// validateManifest enforces the load-time contract (contract §Loading): schema id,
// non-empty version, unique ids, a valid mode, at least one compilable phrase, and
// a well-formed emit whose every referenced name is in a closed registry.
func validateManifest(man *Manifest) error {
	if man.Schema != manifestSchema {
		return fmt.Errorf("manifest schema %q != %q", man.Schema, manifestSchema)
	}
	if strings.TrimSpace(man.Version) == "" {
		return fmt.Errorf("manifest version is required")
	}
	if len(man.Commands) == 0 {
		return fmt.Errorf("manifest has no commands (would fail open to zero)")
	}
	seen := map[string]bool{}
	for i := range man.Commands {
		c := &man.Commands[i]
		if strings.TrimSpace(c.ID) == "" {
			return fmt.Errorf("command #%d: id is required", i)
		}
		if seen[c.ID] {
			return fmt.Errorf("command %q: duplicate id", c.ID)
		}
		seen[c.ID] = true
		if err := validateMode(c.Mode); err != nil {
			return fmt.Errorf("command %q: %w", c.ID, err)
		}
		if err := validatePhrases(c.Phrases, MatchMode(c.Mode)); err != nil {
			return fmt.Errorf("command %q: %w", c.ID, err)
		}
		if err := validateEmit(&c.Emit); err != nil {
			return fmt.Errorf("command %q: %w", c.ID, err)
		}
		if err := validateCommandPlatforms(c); err != nil {
			return fmt.Errorf("command %q: %w", c.ID, err)
		}
	}
	return nil
}

func validateMode(mode string) error {
	switch MatchMode(mode) {
	case ModeWhole, ModeTrailing, ModeAnywhere, ModeTrailingCursor:
		return nil
	default:
		return fmt.Errorf("invalid mode %q (want whole|trailing|anywhere|trailing-cursor)", mode)
	}
}

// validatePhrases requires at least one non-blank phrase and that every non-blank
// phrase compiles under the given mode (the same compile the engine caches).
func validatePhrases(phrases []string, mode MatchMode) error {
	got := 0
	for _, p := range phrases {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if modeRegex(phraseCore(p), mode) == nil {
			return fmt.Errorf("phrase %q does not compile", p)
		}
		got++
	}
	if got == 0 {
		return fmt.Errorf("no compilable phrases")
	}
	return nil
}

// validateEmit checks a command's behavior is well-formed and closed-vocabulary.
func validateEmit(emit *Emit) error {
	switch emit.Kind {
	case "sequence":
		if len(emit.Actions) == 0 {
			return fmt.Errorf("sequence emit has no actions")
		}
		switch emit.OnResolveFail {
		case "", "fallthrough", "noop", "hint":
		default:
			return fmt.Errorf("invalid onResolveFail %q", emit.OnResolveFail)
		}
		for j, ea := range emit.Actions {
			if !actionVerbs[ea.Verb] {
				return fmt.Errorf("action #%d: unknown verb %q", j, ea.Verb)
			}
			for name, expr := range ea.Args {
				if err := validateArgExpr(ea.Verb, name, expr); err != nil {
					return fmt.Errorf("action #%d (%s): %w", j, ea.Verb, err)
				}
			}
		}
		return nil
	case "handler":
		if !handlerRegistry[emit.Handler] {
			return fmt.Errorf("unknown handler %q", emit.Handler)
		}
		return validateHandlerConfig(emit.Handler, emit.Config)
	default:
		return fmt.Errorf("invalid emit kind %q (want sequence|handler)", emit.Kind)
	}
}

// validateArgExpr checks a resolve/transform name is registered and that the
// verb accepts the arg name (the same verb→field mapping the interpreter uses).
func validateArgExpr(verb, name string, expr ArgExpr) error {
	switch expr.Kind {
	case argResolve:
		if _, ok := resolverRegistry[expr.Name]; !ok {
			return fmt.Errorf("arg %q references unknown resolver %q", name, expr.Name)
		}
	case argTransform:
		if _, ok := transformRegistry[expr.Name]; !ok {
			return fmt.Errorf("arg %q references unknown transform %q", name, expr.Name)
		}
	}
	if !verbAcceptsArg(verb, name) {
		return fmt.Errorf("verb %q does not accept arg %q", verb, name)
	}
	return nil
}

// validateHandlerConfig type-checks a handler's static config so a bad config is
// rejected at load, not discovered at fire time.
func validateHandlerConfig(handler string, cfg json.RawMessage) error {
	switch handler {
	case "submit":
		if len(cfg) == 0 {
			return nil // handler defaults apply
		}
		dec := json.NewDecoder(bytes.NewReader(cfg))
		dec.DisallowUnknownFields()
		var sc submitConfig
		if err := dec.Decode(&sc); err != nil {
			return fmt.Errorf("submit config: %w", err)
		}
		if sc.DelayMs < 0 {
			return fmt.Errorf("submit config: delayMs must be >= 0")
		}
	}
	return nil
}
