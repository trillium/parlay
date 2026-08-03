package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// agentHomeDir resolves ~/.parlay/agents/<id>, honoring PARLAY_AGENT_HOME
// like context-reset's receipt path does.
func agentHomeDir(agentID string) string {
	base := os.Getenv("PARLAY_AGENT_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".parlay", "agents")
	}
	return filepath.Join(base, agentID)
}

// writeAgentContext writes context.json (primary agent-lookup file) and a
// session-start epoch sentinel that say-guard reads to distinguish
// inherited handoffs from ones this agent created itself. Both best-effort,
// matching bash's `|| true` (bin/parlay-spawn lines 350–357).
func writeAgentContext(agentID, name, color string) {
	dir := agentHomeDir(agentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	buf, err := json.Marshal(map[string]string{"id": agentID, "name": name, "color": color})
	if err == nil {
		_ = os.WriteFile(filepath.Join(dir, "context.json"), buf, 0o644)
	}
	_ = os.WriteFile(filepath.Join(dir, "session-start"), []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o644)
}
