// Package sayguard is the create->submit death-window guard for chat sends.
//
// Ported from packages/cli/src/say-guard.ts. Forensics on a prior Mayor's
// abrupt death: it ran `handoff create` but posted a courtesy `parlay say`
// before `identity --submit`, and died in that gap. Recovery only worked
// because the identity pointer is pinned at CREATE time. To make the atomic
// create->submit path the SUPPORTED one, `parlay say`/`reply` warns (loudly,
// on stderr — never blocking) whenever it is called while an OPEN handoff
// for the agent has not yet been submitted.
//
// robots-3yy: a fresh context reusing the same agent-id also sees any
// prior, unsubmitted handoff as "open" — the same nag fires even though the
// fresh context did NOT create that handoff and MUST NOT run `identity
// --submit` (that would reset a healthy context). Fix: classify handoffs as
// "inherited" (created before this session started) and emit a distinct,
// non-alarming warning that points to `--dismiss-handoff` instead of
// `--submit`.
package sayguard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/resolvehandoff"
)

func agentHome() string {
	if h := os.Getenv("PARLAY_AGENT_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".parlay", "agents")
}

var pinnedRe = regexp.MustCompile(`📎 Handoff:\s*(\S+)`)

// PinnedHandoffID reads the handoff pointer currently pinned in an agent's
// identity.md, if any. `identity --submit` pins "> 📎 Handoff: <id> — …"; a
// pinned pointer means the agent's shutdown handoff was already submitted.
// Returns "" when the file or pointer is absent. Never panics (a bad read
// must not block a chat send).
func PinnedHandoffID(agent string) string {
	data, err := os.ReadFile(filepath.Join(agentHome(), agent, "identity.md"))
	if err != nil {
		return ""
	}
	m := pinnedRe.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(string(m[1]))
}

// ReadSessionStartMs reads the epoch-ms timestamp written by `parlay spawn` to
// ~/.parlay/agents/<id>/session-start. ok is false when the file is absent
// or unparseable. Never panics.
func ReadSessionStartMs(agent string) (ms int64, ok bool) {
	data, err := os.ReadFile(filepath.Join(agentHome(), agent, "session-start"))
	if err != nil {
		return 0, false
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}
	return sec * 1000, true
}

// WriteSessionStartOnce writes a session-start sentinel if one does not yet
// exist for agent. Called on first `parlay say` as a fallback for agents not
// spawned via `parlay spawn`. The sentinel marks "this session began now" so
// any older open handoff is classified as inherited. Never panics (best
// effort — a guard failure must never block a chat send).
func WriteSessionStartOnce(agent string) {
	dir := filepath.Join(agentHome(), agent)
	file := filepath.Join(dir, "session-start")
	if _, err := os.Stat(file); err == nil {
		return // already set (by `parlay spawn` or a prior first-say)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(file, []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o644)
}

// WarnIfUnsubmittedHandoff prints a stderr warning when a chat message is
// posted while an OPEN handoff exists for agent that has NOT yet been
// submitted. Two distinct cases:
//
//   - Inherited handoff (predates this session): the agent did NOT create
//     it. A fresh context reusing the same agent-id must NOT run `identity
//     --submit` — that resets a healthy context. Shows a calm one-liner
//     pointing to `--dismiss-handoff`.
//   - Current-session handoff (created THIS session, not yet submitted):
//     the agent is posting chat inside the create->submit death window.
//     Shows the aggressive shutdown nag.
//
// WARN ONLY — never blocks the send. Never panics.
func WarnIfUnsubmittedHandoff(agent string) {
	defer func() { _ = recover() }()

	WriteSessionStartOnce(agent)
	var sessionStartedAt *int64
	if ms, ok := ReadSessionStartMs(agent); ok {
		sessionStartedAt = &ms
	}
	result, ok := resolvehandoff.DetectUnsubmittedHandoff(PinnedHandoffID(agent), "", agent, sessionStartedAt)
	if !ok {
		return
	}

	if result.Inherited {
		fmt.Fprintf(os.Stderr,
			"💡 parlay: inherited stale handoff %s for agent '%s' (from a prior session).\n"+
				"    To silence this warning without resetting context: identity --dismiss-handoff %s\n"+
				"    To inspect it: handoff show %s\n",
			result.ID, agent, result.ID, result.ID)
	} else {
		fmt.Fprintf(os.Stderr,
			"⚠️  parlay: open handoff %s for %s is NOT yet submitted.\n"+
				"    You are posting chat inside the create→submit window — the exact gap that\n"+
				"    stranded a prior shutdown. Make it atomic: run `identity --submit` NOW\n"+
				"    (it auto-resolves %s), or `identity --submit %s` to be explicit.\n",
			result.ID, agent, result.ID, result.ID)
	}
}
