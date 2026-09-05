// Package resolvehandoff auto-resolves an agent's current open handoff bead
// id, so `identity --submit`/`--park`/`--handoff` can be given no id at all.
//
// Ported from packages/cli/src/resolve-handoff.ts. This is the fix for the
// create->submit death window: a persistent agent's clean shutdown is two
// acts — (1) `handoff create` mints a bead, (2) `identity --submit <id>`
// pins the pointer AND reincarnates. If anything is interposed between them
// (a courtesy `parlay say`, a context-limit kill), step 2 never runs and the
// shutdown is stranded with a live bead but no pin/restart. Making the id
// optional (resolved here from the store) lets `handoff create … && identity
// --submit` run as ONE atomic act, and lets a bare `identity --submit`
// recover a create that already landed but never got submitted.
package resolvehandoff

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultStore is the store CLI name used when the caller doesn't override
// it — the bead-id prefix ("handoff-1bk" -> `handoff`).
const DefaultStore = "handoff"

// openStatuses are the statuses a live handoff can be in. A "current"
// handoff that has already been closed must never be treated as the pending
// shutdown target.
const openStatuses = "open,in_progress,blocked"

// inheritedAgeMs is the age threshold for the "inherited handoff" fallback
// when no session-start file is available. A handoff older than this was
// almost certainly created in a prior session.
const inheritedAgeMs = 24 * 60 * 60 * 1000

// handoffRow is one handoff row as returned by the store's --json output.
// The bd/handoff store emits `created_at` (ISO8601); `created` is kept as a
// legacy alias so older stores and hand-built test rows still resolve.
// Reading only `created` was robots-qkr: the real field is `created_at`, so
// age was ALWAYS unknown and every inherited handoff misfired the aggressive
// create->submit nag.
type handoffRow struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Assignee  string `json:"assignee"`
	CreatedAt string `json:"created_at"`
	Created   string `json:"created"`
}

// runStore shells out to `<store> <args...>` and parses its stdout as
// either a JSON array of rows or a single JSON object row. Never throws: a
// spawn failure, non-zero exit, empty stdout, or unparseable JSON all
// resolve to (nil, false) so callers demand an explicit id rather than
// guessing.
func runStore(store string, args []string) ([]handoffRow, bool) {
	out, err := exec.Command(store, args...).Output()
	if err != nil || len(out) == 0 {
		return nil, false
	}
	var rows []handoffRow
	if err := json.Unmarshal(out, &rows); err == nil {
		return rows, true
	}
	var single handoffRow
	if err := json.Unmarshal(out, &single); err == nil {
		return []handoffRow{single}, true
	}
	return nil, false
}

// resolveRow resolves the full handoffRow for the newest open bead (so
// callers can inspect its created/created_at for age-based inherited
// detection).
//
// When the agent is KNOWN, the agent-scoped query is AUTHORITATIVE: its
// answer — including "no open handoff for this agent" — is final, and we
// MUST NOT fall through to the store-global newest-open handoff. That
// fall-through was the fleet-wide misattribution bug (robots-4x9f, and its
// root-cause cluster robots-6wb/0sv/bu8/51s/vi7): a fresh/resumed agent has
// zero open handoffs of its OWN, so the store-global fallback grabbed some
// OTHER agent's newest open handoff (136 open store-wide) and pinned the
// create->submit / say-guard nag on whoever was posting. Every fresh agent
// then narrated "stale/inherited handoff unrelated to my role — dismiss it".
// Handoffs set assignee=<agent-id> (owner stays the principal), so the
// agent-scoped list is reliable; its emptiness genuinely means "nothing
// unsubmitted for this agent". Query preference:
//  1. agent known -> list --assignee <agent> --status open,… (AUTHORITATIVE)
//  2. agent UNKNOWN only -> list --status open,…  -> newest open in the store
//  3. agent UNKNOWN only -> show --current        -> store's "current" (may be closed)
//
// The store-global steps 2/3 run ONLY when there is no agent identity to
// scope by (a bare CLI call with no PARLAY_AGENT_ID) — so there is no one to
// misattribute the result to.
func resolveRow(store, agent string) (handoffRow, bool) {
	if agent != "" {
		rows, ok := runStore(store, []string{
			"list", "--status", openStatuses, "--assignee", agent, "--sort", "updated", "-r", "--json",
		})
		if ok && len(rows) > 0 && strings.TrimSpace(rows[0].ID) != "" {
			return rows[0], true
		}
		return handoffRow{}, false
	}

	anyRows, ok := runStore(store, []string{
		"list", "--status", openStatuses, "--sort", "updated", "-r", "--json",
	})
	if ok && len(anyRows) > 0 && strings.TrimSpace(anyRows[0].ID) != "" {
		return anyRows[0], true
	}

	current, ok := runStore(store, []string{"show", "--current", "--json"})
	if ok && len(current) > 0 {
		status := strings.ToLower(strings.TrimSpace(current[0].Status))
		if status != "closed" {
			return current[0], true
		}
	}
	return handoffRow{}, false
}

// ResolveCurrentHandoff resolves the newest OPEN handoff bead id for agent
// (or the store's current, if agent is ""). store == "" defaults to
// DefaultStore; agent == "" defaults to PARLAY_AGENT_ID. Returns "" when the
// store is unavailable or reports nothing open. Never panics.
func ResolveCurrentHandoff(store, agent string) string {
	if store == "" {
		store = DefaultStore
	}
	ag := strings.TrimSpace(agent)
	if ag == "" {
		ag = strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID"))
	}
	row, ok := resolveRow(store, ag)
	if !ok {
		return ""
	}
	return strings.TrimSpace(row.ID)
}

func parseCreatedMs(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UnixMilli(), true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli(), true
	}
	return 0, false
}

// isInherited reports whether row predates the current agent session.
// Primary signal: sessionStartedAt (epoch ms, from
// ~/.parlay/agents/<id>/session-start, written by `parlay spawn` on every new
// spawn). Fallback: row's created age vs the 24h inheritedAgeMs threshold.
func isInherited(row handoffRow, sessionStartedAt *int64) bool {
	// Prefer the store's real created_at; fall back to the legacy created alias.
	created := row.CreatedAt
	if created == "" {
		created = row.Created
	}
	createdMs, ok := parseCreatedMs(created)
	if sessionStartedAt != nil && ok {
		return createdMs < *sessionStartedAt
	}
	if ok {
		return time.Now().UnixMilli()-createdMs > inheritedAgeMs
	}
	// Unknown age -> treat as inherited (robots-qkr). The two warnings are
	// asymmetric: the "current-session" branch urges `identity --submit`,
	// which RESETS context and (on a handoff this agent did not create)
	// corrupts its identity pointer; the "inherited" branch only points to
	// the non-destructive `--dismiss-handoff`. When we cannot prove the
	// handoff belongs to THIS session, the safe default is the gentle,
	// reversible one — never push a destructive reset on an unprovable handoff.
	return true
}

// UnsubmittedResult is the outcome of DetectUnsubmittedHandoff: an open
// handoff bead not yet pinned, and whether it predates this session.
type UnsubmittedResult struct {
	ID        string
	Inherited bool
}

// DetectUnsubmittedHandoff detects a handoff that was created but NOT yet
// submitted — the exact hazard state the atomic create->submit contract
// guards against. "Not yet submitted" is read off the agent's identity.md:
// `identity --submit` pins a "> 📎 Handoff: <id>" pointer, so an OPEN
// handoff for this agent whose id is NOT the pinned pointer is an
// in-flight, unsubmitted shutdown.
//
// Returns (result, false) when nothing open / already pinned. Never panics.
//   - pinnedPointer: the id currently pinned in identity.md ("" if none).
//   - sessionStartedAt: epoch ms this agent session started, from
//     the spawn pipeline's session-start sentinel file; nil when absent (falls
//     back to the 24h age threshold to distinguish inherited handoffs).
//   - Inherited == true means the handoff predates this session; the agent
//     did NOT create it and should NOT run `identity --submit` (that would
//     reset a fresh context) — use `identity --dismiss-handoff` instead.
func DetectUnsubmittedHandoff(pinnedPointer, store, agent string, sessionStartedAt *int64) (UnsubmittedResult, bool) {
	if store == "" {
		store = DefaultStore
	}
	ag := strings.TrimSpace(agent)
	if ag == "" {
		ag = strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID"))
	}
	row, ok := resolveRow(store, ag)
	if !ok {
		return UnsubmittedResult{}, false
	}
	id := strings.TrimSpace(row.ID)
	if id == "" {
		return UnsubmittedResult{}, false
	}
	if pinnedPointer != "" && id == strings.TrimSpace(pinnedPointer) {
		return UnsubmittedResult{}, false
	}
	return UnsubmittedResult{ID: id, Inherited: isInherited(row, sessionStartedAt)}, true
}
