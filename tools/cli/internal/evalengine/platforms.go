package evalengine

import "fmt"

// ── Platform scoping: which surfaces a command runs on, and what they support ────
//
// A "platform" is a surface the engine can drive: the Parlay chat panel, a Herdr
// window, and (later) others. The same phrase can be eligible on several surfaces
// while meaning a surface-specific effect — "change inside input" clears the FOCUSED
// input of whatever surface issued the request, so on Herdr it clears Herdr's input,
// with zero Parlay-visual coupling.
//
// The action verb stays ABSTRACT (clear = "empty the focused input of this
// surface"); the platform's own executor/dispatcher makes it concrete. This file is
// the closed MACHINERY that (a) filters which commands are eligible for a request's
// platform and (b) validates, at load, that a command scoped to a platform only
// emits verbs/handlers that platform actually implements — the same auditable
// coupling the verb registry gives us, now per-surface.
//
// This is the SCOPING dimension only. Whether the engine drives a surface through
// that surface's own client (like Parlay's browser dispatcher today) or reaches into
// it headlessly (the autonomous, non-agent path) is a separate DISPATCH layer that
// builds on top of this — it does not change the scoping model here.

// defaultPlatform is the surface a command (or request) targets when none is named.
// It is "parlay" for backward compatibility: today every command is a Parlay command
// and no caller sends a platform, so the ten builtins and existing callers keep
// working unchanged. A command opts INTO other surfaces explicitly via `platforms`.
const defaultPlatform = "parlay"

// platformDef is a surface's capability set: the action verbs and stateful handlers
// it implements. A command scoped to a platform may reference only names in these
// sets (checked at load).
type platformDef struct {
	verbs    map[string]bool
	handlers map[string]bool
}

// herdrVerbs is Herdr's supported action verbs — a generic text-input surface. It
// implements the input-buffer effects (clear/setText/submitNow) plus noop and the
// hint affordances, and deliberately NOT the Parlay-visual verbs (tab switching,
// navigation, the channel picker) or the speech verbs. This is a starting set to be
// refined when Herdr's real interface lands; narrowing it only tightens validation.
var herdrVerbs = map[string]bool{
	"clear":        true,
	"setText":      true,
	"submitNow":    true,
	"noop":         true,
	"showHint":     true,
	"clearHint":    true,
	"replaceRange": true,
}

// platformRegistry is the closed set of known surfaces. "parlay" is the full
// surface (every action verb + every handler); "herdr" is the text-input surface.
// A command scoped to a platform not in this map is rejected at load.
var platformRegistry = map[string]platformDef{
	"parlay": {verbs: actionVerbs, handlers: handlerRegistry},
	"herdr":  {verbs: herdrVerbs, handlers: map[string]bool{}},
}

// effectivePlatforms is the surfaces a command targets: its explicit `platforms`, or
// the default when none is declared (len 0 → [defaultPlatform]).
func effectivePlatforms(c *CommandManifest) []string {
	if len(c.Platforms) == 0 {
		return []string{defaultPlatform}
	}
	return c.Platforms
}

// requestPlatform is the surface a request targets, defaulting when the client sends
// none (backward compatibility for existing Parlay callers).
func requestPlatform(req EvalRequest) string {
	if req.Platform == "" {
		return defaultPlatform
	}
	return req.Platform
}

// platformEligible reports whether a command should be consulted for a given
// request platform — the eligibility filter the pass applies.
func platformEligible(c *CommandManifest, platform string) bool {
	for _, p := range effectivePlatforms(c) {
		if p == platform {
			return true
		}
	}
	return false
}

// validateCommandPlatforms enforces, at load, that every platform a command targets
// is registered and actually implements what the command emits: a sequence's verbs
// must all be in the platform's verb set; a handler command's handler must be in the
// platform's handler set. This is what makes "a Herdr-scoped command that emits
// openChannelPicker" a load-time rejection rather than a runtime surprise.
func validateCommandPlatforms(c *CommandManifest) error {
	for _, p := range effectivePlatforms(c) {
		def, ok := platformRegistry[p]
		if !ok {
			return fmt.Errorf("unknown platform %q", p)
		}
		switch c.Emit.Kind {
		case "sequence":
			for _, ea := range c.Emit.Actions {
				if !def.verbs[ea.Verb] {
					return fmt.Errorf("verb %q is not supported on platform %q", ea.Verb, p)
				}
			}
		case "handler":
			if !def.handlers[c.Emit.Handler] {
				return fmt.Errorf("handler %q is not supported on platform %q", c.Emit.Handler, p)
			}
		}
	}
	return nil
}
