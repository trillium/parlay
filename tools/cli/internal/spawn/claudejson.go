package spawn

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
		fmt.Fprintf(os.Stderr, "parlay spawn: warn: could not pre-trust %s (parse failed)\n", cwd)
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
		fmt.Fprintf(os.Stderr, "parlay spawn: warn: could not pre-trust %s (encode failed)\n", cwd)
		return
	}

	tmp, err := os.CreateTemp(filepath.Dir(claudeJSONPath), ".claude.json.tmp-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "parlay spawn: warn: could not pre-trust %s (tempfile failed)\n", cwd)
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "parlay spawn: warn: could not pre-trust %s (write failed)\n", cwd)
		return
	}
	// Sync before the rename, and check Close. This is a read-modify-write of
	// the WHOLE of ~/.claude.json, so a rename that becomes visible while the
	// data is still only in the page cache does not lose one setting — it
	// replaces the captain's entire Claude Code state with a truncated file.
	// Close's error was also being discarded, which meant a write failure that
	// only surfaces at close was dropped and then published by the rename.
	// See packages/go-server/internal/atomicfile/atomicfile.go for the
	// reference implementation and the reasoning; this file is in a different
	// Go module and cannot import it.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "parlay spawn: warn: could not pre-trust %s (sync failed)\n", cwd)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "parlay spawn: warn: could not pre-trust %s (close failed)\n", cwd)
		return
	}
	if err := os.Rename(tmpPath, claudeJSONPath); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "parlay spawn: warn: could not pre-trust %s (rename failed)\n", cwd)
	}
}
