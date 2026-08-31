// parlay gc-resolve — resolve a parlay agent id to its Gas City session
// through the bead-backed address directory (spawn-lift unit 7, epic
// task-4cfpv.9).
//
// The identity mapping is dual-written at spawn: `identity --register
// --gc-session <bead-id> --gc-city <dir>` stamps the session pointer into
// identity.md, but identity.md is only the PROJECTION. The source of truth is
// the city's bead store — gc's internal/session AddressDirectory doctrine at
// the pin (third_party/gascity/PIN): "Session state is bead-backed; runtime
// state is observed through providers; work must survive session churn."
// Because the identity lives in beads, not in the supervisor's memory or a
// file next to the process, an id stamped before a supervisor restart still
// resolves after it.
//
// parlay talks to gc across the CLI boundary, so this verb mirrors
// AddressDirectory's documented resolution order:
//
//  1. exact bead-id match on the stamped gc_session, CLOSED SESSIONS
//     INCLUDED — "an identity stamped from a session that has since retired
//     still resolves". This leg is an explicit `gc beads show <id>` fetch,
//     NEVER a session-list scan: the pin's list fallback intentionally never
//     loads closed history (loadSessionBeadSnapshot — "Callers that need a
//     closed record must fetch that one ID explicitly"), so the moment a
//     session retires it vanishes from `session list --state all` and only
//     the single-ID fetch can still see it.
//  2. else live match on "parlay.<agent-id>" over `gc session list --state
//     all --json` — against the row's template (what `gc session new
//     parlay.<id>` actually records: at the pin the argument is the TEMPLATE
//     and the generated session_name is "<id>-adhoc-<hash>", so a bare
//     session_name match can never see a gc-spawn-created session) or, for
//     robustness, an exact session_name. An ambiguous match — two live
//     sessions claiming it — is an error, never a guess: resolving to the
//     wrong session is worse than not resolving (the directory's own safety
//     property).
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/cityscaffold"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/identity"
)

// gcResolveTimeout bounds each gc call (`beads show`, `session list`). The
// no-controller fallback reads the bead store directly, which may spin the
// managed dolt from cold.
const gcResolveTimeout = 60 * time.Second

// gcResolveResult is the typed --json envelope. OK means the agent resolved
// to exactly one session; Via says which directory rule matched ("bead-id"
// or "session-name").
type gcResolveResult struct {
	OK          bool   `json:"ok"`
	AgentID     string `json:"agent_id"`
	SessionID   string `json:"session_id,omitempty"`
	SessionName string `json:"session_name,omitempty"`
	State       string `json:"state,omitempty"`
	Closed      bool   `json:"closed"`
	Via         string `json:"via,omitempty"`
	City        string `json:"city,omitempty"`
	Stamped     string `json:"stamped,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// gcSessionRow is the subset of gc's sessionListJSONRow this verb reads.
type gcSessionRow struct {
	ID          string `json:"id"`
	SessionName string `json:"session_name"`
	Template    string `json:"template"`
	State       string `json:"state"`
	Closed      bool   `json:"closed"`
}

// gcBead is the subset of gc's beads.Bead wire form the bead-id rule reads.
// Metadata is map[string]any, not map[string]string, for the same reason the
// pin's own Bead type uses a coercing StringMap: the external bd CLI can
// type-infer `--set-metadata key=true` into a JSON boolean, and a strict
// string-map decode of one such value would poison the whole bead.
type gcBead struct {
	ID       string         `json:"id"`
	Status   string         `json:"status"` // "open", "in_progress", "closed"
	Metadata map[string]any `json:"metadata"`
}

// metaString reads a string-valued metadata key, returning "" for missing or
// non-string values.
func (b gcBead) metaString(key string) string {
	if s, ok := b.Metadata[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// gcStampedSession reads the gc_session pointer from the agent's identity.md
// projection. Missing file or missing key is fine — resolution falls back to
// the session-name rule.
func gcStampedSession(agentID string) string {
	file := filepath.Join(identity.AgentsRoot(), agentID, "identity.md")
	return strings.TrimSpace(identity.ReadFrontmatter(file).Get("gc_session"))
}

// gcResolveShowBead fetches ONE bead by id via `gc beads show <id>
// --format=json` — the closed-inclusive leg of the directory order. A list
// scan can never satisfy it: the pin's session-list fallback intentionally
// never loads closed history, so the explicit single-ID fetch is the only
// mechanism that still sees a retired session. Two output shapes are
// accepted: the API routing path wraps the bead in {"bead": ...,
// "_cache_age_s": ...}; the direct-store fallback emits the bare bead.
// (--format=json, not --json: gc reserves the bare --json flag in its
// JSON-contract layer and does not wire it for this command.)
//
// Any failure — gc missing, non-zero exit, unparsable output, id mismatch —
// demotes to (zero, false) so resolution falls through to the live
// session-name rule, whose own error reporting surfaces a genuinely broken
// gc; a merely-absent bead must not abort resolution.
func gcResolveShowBead(cityDir, beadID string) (gcBead, bool) {
	bin, _ := gcResolve()
	if bin == "" {
		return gcBead{}, false
	}
	home, err := gcSpawnHome()
	if err != nil {
		return gcBead{}, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), gcResolveTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--city", cityDir, "beads", "show", beadID, "--format=json")
	cmd.Dir = home
	cmd.Env = gcSpawnEnv(home)
	out, err := cmd.Output()
	if err != nil {
		return gcBead{}, false
	}

	var env struct {
		Bead *gcBead `json:"bead"`
	}
	if json.Unmarshal(out, &env) == nil && env.Bead != nil {
		return *env.Bead, env.Bead.ID == beadID
	}
	var bare gcBead
	if json.Unmarshal(out, &bare) == nil && bare.ID == beadID {
		return bare, true
	}
	return gcBead{}, false
}

// gcResolveSessions runs `gc --city <dir> session list --state all --json`
// and returns the rows for the live session-name rule.
func gcResolveSessions(cityDir string) ([]gcSessionRow, error) {
	bin, _ := gcResolve()
	if bin == "" {
		return nil, fmt.Errorf("gc not found (PARLAY_GC unset, none on PATH) — %s", gcInstallFix)
	}
	home, err := gcSpawnHome()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), gcResolveTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--city", cityDir, "session", "list", "--state", "all", "--json")
	cmd.Dir = home
	cmd.Env = gcSpawnEnv(home)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()

	var list struct {
		Sessions []gcSessionRow `json:"sessions"`
	}
	if jsonErr := json.Unmarshal(out, &list); jsonErr != nil {
		return nil, fmt.Errorf("gc session list did not emit typed JSON (run err: %v): stdout %q, stderr %q", runErr, strings.TrimSpace(string(out)), strings.TrimSpace(stderr.String()))
	}
	if runErr != nil {
		return nil, fmt.Errorf("gc session list failed (err: %v): %s", runErr, strings.TrimSpace(stderr.String()))
	}
	return list.Sessions, nil
}

// gcResolveRun is the testable core: the AddressDirectory resolution order
// over the CLI boundary.
func gcResolveRun(agentID string) (gcResolveResult, error) {
	res := gcResolveResult{AgentID: agentID}

	cityDir := cityscaffold.Dir()
	res.City = cityDir
	res.Stamped = gcStampedSession(agentID)

	// Rule 1: exact bead-id match on the stamped pointer, closed included —
	// a retired session's identity still resolves. This must be the explicit
	// single-ID fetch: session list drops closed history by design.
	if res.Stamped != "" {
		if bead, found := gcResolveShowBead(cityDir, res.Stamped); found {
			res.OK = true
			res.SessionID = bead.ID
			res.SessionName = bead.metaString("session_name")
			res.State = bead.metaString("state")
			res.Closed = bead.Status == "closed"
			res.Via = "bead-id"
			return res, nil
		}
	}

	sessions, err := gcResolveSessions(cityDir)
	if err != nil {
		return res, err
	}

	// Rule 2: live match on gc-spawn's canonical name. The pin records the
	// `session new parlay.<id>` argument as the row's TEMPLATE and generates
	// session_name "<id>-adhoc-<hash>", so the template is the field a
	// gc-spawn-created session actually carries; the session_name leg is kept
	// for exact-named sessions. Two live claimants is ambiguity, never a coin
	// flip.
	wantName := "parlay." + agentID
	var matches []gcSessionRow
	for _, s := range sessions {
		if !s.Closed && (s.SessionName == wantName || s.Template == wantName) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 1:
		res.OK = true
		res.SessionID = matches[0].ID
		res.SessionName = matches[0].SessionName
		res.State = matches[0].State
		res.Closed = false
		res.Via = "session-name"
		return res, nil
	case 0:
		if res.Stamped != "" {
			res.Reason = fmt.Sprintf("stamped gc_session %s not in the city's bead store and no live session named or templated %s", res.Stamped, wantName)
		} else {
			res.Reason = fmt.Sprintf("no gc_session stamped in identity.md and no live session named or templated %s", wantName)
		}
		return res, nil
	default:
		return res, fmt.Errorf("ambiguous: %d live sessions claim %s — refusing to guess (resolving to the wrong session is worse than not resolving)", len(matches), wantName)
	}
}

// GCResolve implements `parlay gc-resolve <agent-id> [--json]`. Exit codes:
// 0 resolved; 1 not resolved (no match, ambiguity, or error).
func GCResolve(argv []string) {
	if helpWanted("gc-resolve", argv) {
		return
	}
	r := args.Parse("gc-resolve", argv, []string{"--json"}, nil)
	asJSON := r.Bool("--json")
	if len(r.Positionals) != 1 {
		httpc.Die("parlay gc-resolve: usage: parlay gc-resolve <agent-id> [--json]", config.ExitUsage)
		return
	}

	res, err := gcResolveRun(r.Positionals[0])
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay gc-resolve: %v", err), config.ExitRuntime)
		return
	}
	if asJSON {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
	} else if res.OK {
		fmt.Printf("resolved %s -> gc session %s (%s, state %s, via %s)\n", res.AgentID, res.SessionID, res.SessionName, res.State, res.Via)
	} else {
		fmt.Printf("unresolved: %s\n", res.Reason)
	}
	if !res.OK {
		os.Exit(config.ExitRuntime)
	}
}
