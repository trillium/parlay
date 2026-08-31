// Crew-store dual-write plumbing (status-lift unit 3): the seam that makes
// `parlay status` and claim's failure recorder ALSO land each status write in
// (a) the agent's per-agent event log (internal/crewevents — the §7.1
// mitigation: an append that returns its failures instead of silently
// dropping them) and (b) the agent's crew bead in parlay's own beads store
// (internal/parlaybeads, unit-2 schema).
//
// Gate: PARLAY_CREW_STORE=<beadsDir>. Unset (the default everywhere today)
// means the new pipeline does not run at all and behavior stays
// byte-identical — that is the dual-write shakedown switch unit 7's rollout
// plan flips per-host.
//
// Write order per status write: status FILE first (the operative record ~30
// firstmate scripts read — its reliability must not regress), then event
// append, then bead write. A new-pipeline failure after the file landed is
// reported loudly (Q5b: `parlay status` dies EXIT_RUNTIME naming what did
// land; claim's best-effort path notes it on stderr) — never swallowed.
package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/crewevents"
	"github.com/trillium/parlay/tools/cli/internal/identity"
	"github.com/trillium/parlay/tools/cli/internal/parlaybeads"
)

// crewStoreTimeout bounds one bead write. Generous because the first open of
// an embedded dolt store spins it from cold (same reasoning as
// gcResolveTimeout).
const crewStoreTimeout = 60 * time.Second

// crewStoreDir reads the gate: the beadsDir when the bead-backed status
// pipeline is enabled, "" when the write stays file-only.
func crewStoreDir() string { return strings.TrimSpace(os.Getenv("PARLAY_CREW_STORE")) }

// errNoCrewIdentity marks the one structural skip: PARLAY_STATUS_FILE gave
// the write a file sink but no agent id exists to key the event log or crew
// bead. Callers report it (stderr note) rather than dying — it is a
// configuration shape, not a transient failure.
var errNoCrewIdentity = errors.New("no agent identity (PARLAY_AGENT_ID) to key the crew store by")

// crewStoreOpen is the store-opening seam, a var so tests can substitute an
// in-memory Client. The default uses Init, not Open: the writer is the one
// place a crew store may come into existence (libclient.go's "unit-3
// decision") — an agent's first status write on a freshly-gated host must
// not die because no one ran an init verb first.
var crewStoreOpen = func(ctx context.Context, dir, actor string) (parlaybeads.Client, error) {
	return parlaybeads.Init(ctx, parlaybeads.Config{Dir: dir, Actor: actor})
}

// crewDualWrite lands one already-file-recorded status write in the event
// log and the crew bead. No-op (nil) when the gate is off. Every failure
// comes back to the caller; nothing here is fire-and-forget.
func crewDualWrite(agent, verb, key, note string) error {
	dir := crewStoreDir()
	if dir == "" {
		return nil
	}
	if agent == "" {
		return errNoCrewIdentity
	}
	at := time.Now().UTC().Format(time.RFC3339)

	// Event before bead: the event log is the replay/cursor source (units 5
	// and 7); if the bead write then fails, the log still holds the truth a
	// re-run or the reconciler can compare against.
	evFile := crewevents.File(filepath.Join(identity.AgentsRoot(), agent))
	if _, err := crewevents.Append(evFile, crewevents.Event{
		At:    at,
		Name:  crewevents.EventCrewStatus,
		Agent: agent,
		Verb:  verb,
		Key:   key,
		Note:  note,
	}); err != nil {
		return fmt.Errorf("event append: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), crewStoreTimeout)
	defer cancel()
	c, err := crewStoreOpen(ctx, dir, agent)
	if err != nil {
		return fmt.Errorf("opening crew store: %w", err)
	}
	defer c.Close()

	st := parlaybeads.CrewStatus{AgentID: agent, Verb: verb, Key: key, Note: note, At: at}
	// The attachment pointer to the spawn seam's agent record, when this
	// agent was gc-spawned (identity.md carries the stamp). Merged on every
	// write so a stamp that arrives after the first status still lands.
	var extra map[string]string
	if sid := gcStampedSession(agent); sid != "" {
		extra = map[string]string{parlaybeads.KeyGCSession: sid}
	}
	if _, err := parlaybeads.ApplyStatus(ctx, c, st, extra); err != nil {
		return fmt.Errorf("bead write: %w", err)
	}
	return nil
}
