package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// stampIdentityGCSession writes an identity.md projection carrying the
// gc_session pointer (plus a worktree line — the robots-6xq7 field this
// dual-write must never displace) into a temp agent store.
func stampIdentityGCSession(t *testing.T, agentID, sessionID string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("PARLAY_AGENT_HOME", root)
	dir := filepath.Join(root, agentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: " + agentID + "\nworktree: /tmp/wt/" + agentID + "\ngc_session: " + sessionID + "\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "identity.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sessionListJSON(rows ...map[string]any) string {
	out, _ := json.Marshal(map[string]any{"schema_version": "1", "sessions": rows})
	return string(out)
}

// beadShowJSON is `gc beads show --format=json`'s direct-store fallback shape:
// the bare bead. Session name and state live in metadata; closed-ness is the
// bead's own status.
func beadShowJSON(id, status, sessionName, state string) string {
	out, _ := json.Marshal(map[string]any{
		"id":     id,
		"title":  "session " + sessionName,
		"status": status,
		"metadata": map[string]any{
			"session_name": sessionName,
			"state":        state,
		},
	})
	return string(out)
}

// writeResolveFakeGC writes a fake gc answering BOTH verbs gc-resolve can
// issue — `beads show` (rule 1's explicit closed-inclusive fetch) and
// `session list` (rule 2) — with separate canned stdout and per-verb argv
// records. The shared writeSpawnFakeGC prints one canned stdout for every
// invocation and overwrites its argv record per call, which cannot express
// "the bead fetch misses, then the list answers".
func writeResolveFakeGC(t *testing.T, beadsOut string, beadsExit int, listOut string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"stdout-beads": beadsOut, "stdout-list": listOut} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
rec="` + dir + `"
case "$*" in
*"beads show"*)
  printf '%s\n' "$@" > "$rec/argv-beads"
  cat "$rec/stdout-beads"
  exit ` + strconv.Itoa(beadsExit) + ` ;;
*)
  printf '%s\n' "$@" > "$rec/argv-list"
  cat "$rec/stdout-list"
  exit 0 ;;
esac
`
	bin := filepath.Join(dir, "gc")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func TestGCResolveByStampedBeadIDIncludesClosed(t *testing.T) {
	testsupport.TempStateHome(t)
	stampIdentityGCSession(t, "agent-x", "pa-old")
	// The stamped session has RETIRED (closed) and its name has been reused
	// by a live session: the bead-id rule must still win, exactly as the
	// AddressDirectory resolves — an identity stamped from a session that
	// has since retired still resolves. The list scan CANNOT provide this:
	// gc's session-list fallback intentionally never loads closed history
	// ("Callers that need a closed record must fetch that one ID
	// explicitly"), so the rule must be an explicit `beads show` fetch.
	bin, rec := writeResolveFakeGC(t,
		beadShowJSON("pa-old", "closed", "parlay.agent-x", "closed"), 0,
		sessionListJSON(map[string]any{"id": "pa-new", "session_name": "parlay.agent-x", "state": "active", "closed": false}),
	)
	t.Setenv("PARLAY_GC", bin)

	res, err := gcResolveRun("agent-x")
	if err != nil {
		t.Fatalf("gcResolveRun: %v", err)
	}
	if !res.OK || res.SessionID != "pa-old" || res.Via != "bead-id" || !res.Closed {
		t.Errorf("want pa-old via bead-id (closed), got %+v", res)
	}
	if res.SessionName != "parlay.agent-x" || res.State != "closed" {
		t.Errorf("bead metadata not projected: %+v", res)
	}
	argv, readErr := os.ReadFile(filepath.Join(rec, "argv-beads"))
	if readErr != nil {
		t.Fatalf("the explicit single-ID fetch never ran: %v", readErr)
	}
	// --format=json, not --json: gc reserves the bare --json flag and does
	// not wire it for beads show.
	if !strings.Contains(string(argv), "beads\nshow\npa-old\n--format=json") {
		t.Errorf("want an explicit `beads show pa-old --format=json` fetch, argv:\n%s", argv)
	}
	// Rule 1 must short-circuit: the live name-reuser is never consulted.
	if _, err := os.Stat(filepath.Join(rec, "argv-list")); err == nil {
		t.Errorf("session list ran despite a bead-id resolution — the name-reuser could have shadowed the stamp")
	}
}

func TestGCResolveBeadShowAPIEnvelope(t *testing.T) {
	// When a gc controller is alive, `beads show --format=json` routes via
	// the supervisor API and wraps the bead in {"bead": ..., "_cache_age_s"}.
	// Resolution must accept that shape too, not just the bare-bead fallback.
	testsupport.TempStateHome(t)
	stampIdentityGCSession(t, "agent-v", "pa-live")
	wrapped, _ := json.Marshal(map[string]any{
		"bead":         json.RawMessage(beadShowJSON("pa-live", "open", "parlay.agent-v", "active")),
		"_cache_age_s": 2.5,
	})
	bin, _ := writeResolveFakeGC(t, string(wrapped), 0, sessionListJSON())
	t.Setenv("PARLAY_GC", bin)

	res, err := gcResolveRun("agent-v")
	if err != nil {
		t.Fatalf("gcResolveRun: %v", err)
	}
	if !res.OK || res.SessionID != "pa-live" || res.Via != "bead-id" || res.Closed {
		t.Errorf("want pa-live via bead-id (open) from the API envelope, got %+v", res)
	}
}

func TestGCResolveByTemplateWhenUnstamped(t *testing.T) {
	// The pin's real naming: `gc session new parlay.agent-y` records the
	// argument as the TEMPLATE and generates session_name
	// "agent-y-adhoc-<hash>". Rule 2 must match on the template, or no
	// gc-spawn-created session can ever resolve by name (soak pass 1 defect).
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir()) // no identity.md at all
	out := sessionListJSON(
		map[string]any{"id": "pa-7", "session_name": "agent-y-adhoc-d665c919cc", "template": "parlay.agent-y", "state": "active", "closed": false},
		map[string]any{"id": "pa-8", "session_name": "other-adhoc-0badc0ffee", "template": "parlay.other", "state": "active", "closed": false},
	)
	bin, _ := writeSpawnFakeGC(t, out, 0)
	t.Setenv("PARLAY_GC", bin)

	res, err := gcResolveRun("agent-y")
	if err != nil {
		t.Fatalf("gcResolveRun: %v", err)
	}
	if !res.OK || res.SessionID != "pa-7" || res.Via != "session-name" {
		t.Errorf("want pa-7 via session-name (template match), got %+v", res)
	}
}

func TestGCResolveByExactSessionNameWhenUnstamped(t *testing.T) {
	// The exact-session_name leg stays: a session literally named
	// "parlay.<id>" (hand-created or a future gc that honors the name)
	// resolves too.
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	out := sessionListJSON(
		map[string]any{"id": "pa-7", "session_name": "parlay.agent-y", "state": "active", "closed": false},
		map[string]any{"id": "pa-8", "session_name": "parlay.other", "state": "active", "closed": false},
	)
	bin, _ := writeSpawnFakeGC(t, out, 0)
	t.Setenv("PARLAY_GC", bin)

	res, err := gcResolveRun("agent-y")
	if err != nil {
		t.Fatalf("gcResolveRun: %v", err)
	}
	if !res.OK || res.SessionID != "pa-7" || res.Via != "session-name" {
		t.Errorf("want pa-7 via session-name, got %+v", res)
	}
}

func TestGCResolveClosedNameDoesNotResolve(t *testing.T) {
	// The session-name rule is LIVE-only (the directory demotes stale
	// names); only the stamped bead id may resolve a closed session.
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	out := sessionListJSON(
		map[string]any{"id": "pa-9", "session_name": "agent-z-adhoc-1234abcd", "template": "parlay.agent-z", "state": "closed", "closed": true},
	)
	bin, _ := writeSpawnFakeGC(t, out, 0)
	t.Setenv("PARLAY_GC", bin)

	res, err := gcResolveRun("agent-z")
	if err != nil {
		t.Fatalf("gcResolveRun: %v", err)
	}
	if res.OK {
		t.Errorf("closed session must not resolve by name, got %+v", res)
	}
	if !strings.Contains(res.Reason, "no live session named or templated parlay.agent-z") {
		t.Errorf("reason = %q", res.Reason)
	}
}

func TestGCResolveAmbiguousLiveNameErrors(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	// One claims by exact session_name, one by template: the union of both
	// legs still counts as two claimants — ambiguity, never a coin flip.
	out := sessionListJSON(
		map[string]any{"id": "pa-a", "session_name": "parlay.agent-q", "state": "active", "closed": false},
		map[string]any{"id": "pa-b", "session_name": "agent-q-adhoc-9f9f9f", "template": "parlay.agent-q", "state": "active", "closed": false},
	)
	bin, _ := writeSpawnFakeGC(t, out, 0)
	t.Setenv("PARLAY_GC", bin)

	_, err := gcResolveRun("agent-q")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("two live claimants must error, never guess; got err=%v", err)
	}
}

func TestGCResolveStampedMissingFallsBackThenReports(t *testing.T) {
	// The bead fetch missing (gc exits non-zero for an unknown id) demotes to
	// the live-name rule rather than aborting; when that misses too, the
	// reason names the stamped pointer.
	testsupport.TempStateHome(t)
	stampIdentityGCSession(t, "agent-w", "pa-gone")
	bin, rec := writeResolveFakeGC(t, "", 1, sessionListJSON(
		map[string]any{"id": "pa-x", "session_name": "parlay.someone-else", "state": "active", "closed": false},
	))
	t.Setenv("PARLAY_GC", bin)

	res, err := gcResolveRun("agent-w")
	if err != nil {
		t.Fatalf("gcResolveRun: %v", err)
	}
	if res.OK {
		t.Errorf("nothing matches — must not resolve, got %+v", res)
	}
	if !strings.Contains(res.Reason, "pa-gone") {
		t.Errorf("reason should name the stamped id, got %q", res.Reason)
	}
	if res.Stamped != "pa-gone" {
		t.Errorf("envelope should carry the stamped pointer, got %q", res.Stamped)
	}
	// Both directory rules must actually have been consulted.
	for _, f := range []string{"argv-beads", "argv-list"} {
		if _, err := os.Stat(filepath.Join(rec, f)); err != nil {
			t.Errorf("%s missing — that rule never ran: %v", f, err)
		}
	}
}

func TestGCResolveResultEnvelopeShape(t *testing.T) {
	res := gcResolveResult{OK: true, AgentID: "a", SessionID: "s", SessionName: "parlay.a", State: "active", Via: "bead-id", City: "/c", Stamped: "s"}
	out, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ok", "agent_id", "session_id", "session_name", "state", "closed", "via", "city", "stamped"} {
		if _, present := m[key]; !present {
			t.Errorf("envelope missing %q: %s", key, out)
		}
	}
}
