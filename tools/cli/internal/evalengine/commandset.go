package evalengine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	e.mu.Lock()
	e.base = man
	e.commands = compileManifest(withEnders(man, e.extraEnders))
	e.mu.Unlock()
}

// SetExtraEnders appends user-supplied phrases to the submit command and keeps
// them across SetCommands, including a command-file reload.
func (e *Engine) SetExtraEnders(phrases []string) {
	e.mu.Lock()
	e.extraEnders = phrases
	e.commands = compileManifest(withEnders(e.base, phrases))
	e.mu.Unlock()
}

// withEnders appends extra phrases onto the submit command. Other commands are
// unchanged. An empty extra list returns the manifest as-is.
func withEnders(man *Manifest, extra []string) *Manifest {
	if man == nil || len(extra) == 0 {
		return man
	}
	out := *man
	out.Commands = make([]CommandManifest, len(man.Commands))
	copy(out.Commands, man.Commands)
	for i := range out.Commands {
		if out.Commands[i].ID != "submit" {
			continue
		}
		merged := append([]string{}, out.Commands[i].Phrases...)
		out.Commands[i].Phrases = append(merged, extra...)
	}
	return &out
}

// applyFileManifest merges a validated command file onto the embedded default
// and installs that set. The file adds: it does not replace shipped commands.
// An invalid merge leaves the live set untouched.
func applyFileManifest(engine *Engine, man *Manifest) error {
	merged := mergeManifest(embeddedManifest(), man)
	if err := validateManifest(merged); err != nil {
		return fmt.Errorf("merged command file: %w", err)
	}
	engine.SetCommands(merged)
	return nil
}

// mergeManifest adds overlay onto base. An existing command id keeps the base
// mode, priority, platforms, description, and emit; its phrases are the base
// phrases followed by overlay phrases not already present (case-insensitive).
// An id only in the overlay is appended whole. Base commands and base phrases
// are never dropped. Each call starts from base, so a later overlay does not
// keep phrases an earlier overlay added and then removed.
func mergeManifest(base, overlay *Manifest) *Manifest {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	out := *base
	out.Commands = make([]CommandManifest, len(base.Commands))
	index := make(map[string]int, len(base.Commands))
	for i, c := range base.Commands {
		cp := c
		cp.Phrases = append([]string{}, c.Phrases...)
		out.Commands[i] = cp
		index[c.ID] = i
	}
	for _, oc := range overlay.Commands {
		i, ok := index[oc.ID]
		if !ok {
			cp := oc
			cp.Phrases = append([]string{}, oc.Phrases...)
			if oc.Platforms != nil {
				cp.Platforms = append([]string{}, oc.Platforms...)
			}
			out.Commands = append(out.Commands, cp)
			continue
		}
		seen := make(map[string]bool, len(out.Commands[i].Phrases))
		for _, p := range out.Commands[i].Phrases {
			seen[strings.ToLower(strings.TrimSpace(p))] = true
		}
		for _, p := range oc.Phrases {
			key := strings.ToLower(strings.TrimSpace(p))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out.Commands[i].Phrases = append(out.Commands[i].Phrases, strings.TrimSpace(p))
		}
	}
	return &out
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
