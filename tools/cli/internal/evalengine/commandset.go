package evalengine

import (
	"bytes"
	"encoding/json"
	"sort"
)

// compileManifest turns a validated Manifest into the engine's live command set:
// drop disabled commands, sort by priority ascending (lower wins, first match ends
// the pass — registry.ts:21), and compile each command's phrases once. The
// manifest is pre-validated, so every phrase is known to compile.
func compileManifest(man *Manifest) []compiledCommand {
	cmds := make([]compiledCommand, 0, len(man.Commands))
	for _, c := range man.Commands {
		if !c.isEnabled() {
			continue
		}
		cmds = append(cmds, compiledCommand{
			cmd:      c,
			matchers: compilePhrases(c.Phrases, MatchMode(c.Mode)),
		})
	}
	sort.SliceStable(cmds, func(i, j int) bool { return cmds[i].cmd.Priority < cmds[j].cmd.Priority })
	return cmds
}

// SetCommands atomically swaps the engine's live command set from a validated
// manifest (hot-reload). Compilation happens before the lock so the swap itself is
// a single pointer assignment — an in-flight Eval either sees the whole old set or
// the whole new set, never a torn mix. Callers must pass only a validated Manifest;
// an empty set is impossible because validateManifest rejects zero commands (never
// fall open to no commands).
func (e *Engine) SetCommands(man *Manifest) {
	cmds := compileManifest(man)
	e.mu.Lock()
	e.commands = cmds
	e.mu.Unlock()
}

// commandSet resolves which command set a request evaluates against, implementing
// the request > file > embedded precedence. A per-request Commands override is
// compiled and used ONLY if it parses+validates; an invalid override is ignored
// (fail-closed to the live set), never a 400 — the request still evaluates. The
// override is not cached: it is opt-in per request, so the common no-override hot
// path pays nothing and only override-carrying requests pay the compile.
func (e *Engine) commandSet(req EvalRequest) []compiledCommand {
	if raw := bytes.TrimSpace(req.Commands); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if man, err := parseManifest(raw); err == nil {
			return compileManifest(man)
		}
		// Invalid override: fall through to the live file/embedded set.
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.commands
}

// isSubmitHandler reports whether an emit delegates to the stateful submit handler.
func isSubmitHandler(emit Emit) bool {
	return emit.Kind == "handler" && emit.Handler == "submit"
}

// submitDelay reads the submit handler's countdown from its config, defaulting to
// submitDelayMs when unset. Validation already rejected a negative delayMs.
func submitDelay(emit Emit) int {
	if len(emit.Config) == 0 {
		return submitDelayMs
	}
	var sc submitConfig
	if err := json.Unmarshal(emit.Config, &sc); err == nil && sc.DelayMs > 0 {
		return sc.DelayMs
	}
	return submitDelayMs
}

// describeCommands returns the registered command table for /commands (debug).
func (e *Engine) describeCommands() []map[string]any {
	e.mu.Lock()
	cmds := e.commands
	e.mu.Unlock()
	rows := make([]map[string]any, 0, len(cmds))
	for _, cc := range cmds {
		s := cc.cmd
		rows = append(rows, map[string]any{
			"id": s.ID, "priority": s.Priority, "mode": s.Mode,
			"phrases": s.Phrases, "description": s.Description,
			"emit": s.Emit.Kind,
		})
	}
	return rows
}
