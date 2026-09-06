// The fold §3.6 keyed status verb — the agent→supervisor signal. Distinct
// from the bare `parlay` panel/fleet snapshot in status.go: `status` used to
// be a redundant fall-through alias of bare `parlay` (zero unique
// behavior), so the fold retired that alias and bound the name to this verb
// instead (task-ve2v). Output here MUST parse under firstmate's
// fm-classify-lib.sh grammar (verb, optional "[key=<slug>]" between verb and
// colon, note after colon) — statusLine's exact byte shape is load-bearing.
//
// Ported from packages/cli/src/commands-status.ts.
package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/identity"
)

var statusVerbs = []string{"working", "needs-decision", "blocked", "paused", "done", "failed", "resolved"}

var keySlugRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func isStatusVerb(v string) bool {
	for _, sv := range statusVerbs {
		if sv == v {
			return true
		}
	}
	return false
}

// statusSink resolves the CALLING process's own status file: write to
// $PARLAY_STATUS_FILE when set (firstmate injects it at spawn, and its
// fm-watch loop reads that exact file), else the parlay-native default
// ~/.parlay/agents/<id>/status keyed off PARLAY_AGENT_ID. Same verb, two
// homes — the agent code is identical whoever launched it. Dies with
// EXIT_USAGE if neither is available.
func statusSink() (agent, file string) {
	env := strings.TrimSpace(os.Getenv("PARLAY_STATUS_FILE"))
	agent = strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID"))
	if env != "" {
		return agent, env
	}
	if agent == "" {
		httpc.Die("parlay status: no agent identity — set PARLAY_STATUS_FILE, or run inside a parlay-spawned agent (sets PARLAY_AGENT_ID)", config.ExitUsage)
		return "", ""
	}
	dir := filepath.Join(identity.AgentsRoot(), agent)
	_ = os.MkdirAll(dir, 0o755)
	return agent, filepath.Join(dir, "status")
}

// statusFileForAgent resolves a TARGET agent's status file directly from its
// id — ~/.parlay/agents/<agentID>/status — ignoring any env-derived identity
// of the CALLING process. Used by crew-state.
//
// Fidelity fix (ticket B5, pre-approved — see the brief): the TS original's
// crewStateForAgent(agentId) called statusSink() here instead, which
// resolves the CALLER's own PARLAY_AGENT_ID/PARLAY_STATUS_FILE and silently
// ignores the agentId parameter it was passed — so `parlay crew-state <id>`
// never actually read <id>'s status file (it read the caller's own, or died
// if the caller had no identity set). That's a genuine defect against the
// function's documented contract ("an agent's last keyed status line" — the
// one PASSED IN), not an intentional design choice. Fixed here: always
// resolve directly from agentID.
func statusFileForAgent(agentID string) string {
	return filepath.Join(identity.AgentsRoot(), agentID, "status")
}

// buildStatusLine renders one status line in firstmate's exact grammar
// (fm-classify-lib.sh): the optional "[key=<slug>]" token sits between the
// verb and the colon, so fm's status_line_verb / _fm_decision_key parse both
// cleanly.
func buildStatusLine(verb, key, note string) string {
	verbPart := verb
	if key != "" {
		verbPart = fmt.Sprintf("%s [key=%s]", verb, key)
	}
	if note != "" {
		return fmt.Sprintf("%s: %s\n", verbPart, note)
	}
	return fmt.Sprintf("%s:\n", verbPart)
}

// StatusVerb ports cmdStatusVerb: `parlay status <verb> [--key <slug>]
// "<note>"` appends a keyed status line; bare `parlay status` reads THIS
// agent's own status file.
func StatusVerb(argv []string) {
	if helpWanted("status", argv) {
		return
	}
	r := args.Parse("status", argv, nil, []string{"--key"})

	if len(r.Positionals) == 0 {
		_, file := statusSink()
		if _, err := os.Stat(file); err != nil {
			fmt.Println(`(no status yet — write one with: parlay status working "<note>")`)
			return
		}
		data, err := os.ReadFile(file)
		if err != nil {
			httpc.Die(fmt.Sprintf("parlay status: %v", err), config.ExitRuntime)
			return
		}
		os.Stdout.Write(data)
		return
	}

	verb := r.Positionals[0]
	if !isStatusVerb(verb) {
		httpc.Die(fmt.Sprintf("parlay status: unknown verb %q — one of: %s", verb, strings.Join(statusVerbs, ", ")), config.ExitUsage)
		return
	}

	var key string
	if v, present := r.String("--key"); present {
		key = strings.TrimSpace(v)
		if !keySlugRe.MatchString(key) {
			httpc.Die(fmt.Sprintf("parlay status: invalid --key %q — slug chars are [A-Za-z0-9._-]", key), config.ExitUsage)
			return
		}
	}

	note := strings.TrimSpace(strings.Join(r.Positionals[1:], " "))
	agent, file := statusSink()
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay status: %v", err), config.ExitRuntime)
		return
	}
	_, writeErr := f.WriteString(buildStatusLine(verb, key, note))
	closeErr := f.Close()
	if writeErr != nil {
		httpc.Die(fmt.Sprintf("parlay status: %v", writeErr), config.ExitRuntime)
		return
	}
	if closeErr != nil {
		httpc.Die(fmt.Sprintf("parlay status: %v", closeErr), config.ExitRuntime)
		return
	}

	// Dual-write (unit 3, gated on PARLAY_CREW_STORE): the file above is the
	// operative record and has landed; a new-pipeline failure after it is
	// still a loud death (Q5b), phrased so the caller knows the line itself
	// was not lost. The identity-less shape (PARLAY_STATUS_FILE with no
	// PARLAY_AGENT_ID) is structural, not transient — noted, not fatal.
	if dwErr := crewDualWrite(agent, verb, key, note); dwErr != nil {
		if errors.Is(dwErr, errNoCrewIdentity) {
			fmt.Fprintf(os.Stderr, "parlay status: note — crew-store dual-write skipped: %v\n", dwErr)
		} else {
			httpc.Die(fmt.Sprintf("parlay status: status line landed at %s but the crew-store dual-write failed (PARLAY_CREW_STORE=%s): %v", file, crewStoreDir(), dwErr), config.ExitRuntime)
			return
		}
	}

	keyPart := ""
	if key != "" {
		keyPart = fmt.Sprintf(" [key=%s]", key)
	}
	fmt.Printf("status %s%s → %s\n", verb, keyPart, file)
}
