package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// pretrustWorkdir marks cwd as trust-accepted in ~/.claude.json so the
// spawned claude never stalls on the folder trust dialog. bin/parlay-spawn
// step 4 (lines 489–499) — best-effort: a missing file or unparseable JSON
// is a warning, not a fatal error, matching bash's jq-failure fallback.
// Honors PARLAY_CLAUDE_JSON to redirect the target path in tests, like
// agentHomeDir does for PARLAY_AGENT_HOME.
func pretrustWorkdir(cwd string) {
	claudeJSONPath := os.Getenv("PARLAY_CLAUDE_JSON")
	if claudeJSONPath == "" {
		claudeJSONPath = filepath.Join(os.Getenv("HOME"), ".claude.json")
	}
	raw, err := os.ReadFile(claudeJSONPath)
	if err != nil {
		return // parity: bash only acts `if [ -f "$CLAUDE_JSON" ]`
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: warn: could not pre-trust %s (parse failed)\n", cwd)
		return
	}

	projects, _ := doc["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	entry, _ := projects[cwd].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	entry["hasTrustDialogAccepted"] = true
	projects[cwd] = entry
	doc["projects"] = projects

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: warn: could not pre-trust %s (encode failed)\n", cwd)
		return
	}

	tmp, err := os.CreateTemp(filepath.Dir(claudeJSONPath), ".claude.json.tmp-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: warn: could not pre-trust %s (tempfile failed)\n", cwd)
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "parlay-spawn: warn: could not pre-trust %s (write failed)\n", cwd)
		return
	}
	tmp.Close()
	if err := os.Rename(tmpPath, claudeJSONPath); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "parlay-spawn: warn: could not pre-trust %s (rename failed)\n", cwd)
	}
}
